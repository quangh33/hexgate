package main

import (
	"crypto/rsa"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/consul/api"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v3"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	GatewayPort    string          `yaml:"gatewayPort"`
	Services       []Service       `yaml:"services"`
	RateLimiting   RateLimitConfig `yaml:"rateLimiting"`
	Authentication AuthConfig      `yaml:"authentication"`
	TLS            TLSConfig       `yaml:"tls"`
}

type RateLimitConfig struct {
	Enabled       bool    `yaml:"enabled"`
	RatePerSecond float64 `yaml:"ratePerSecond"`
	Burst         int     `yaml:"burst"`
}

type Service struct {
	Name              string `yaml:"name"`
	Path              string `yaml:"path"`
	ConsulServiceName string `yaml:"consulServiceName"`
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type AuthConfig struct {
	Enabled       bool   `yaml:"enabled"`
	PublicKeyPath string `yaml:"publicKeyPath"`
}

// Backend represents a single upstream server
type Backend struct {
	URL          *url.URL
	ReverseProxy *httputil.ReverseProxy
	isAlive      atomic.Bool
}

// ServerPool holds the list of available backends
type ServerPool struct {
	backends          map[string]*Backend // Consul Service id -> Backend
	current           uint64
	mu                sync.RWMutex
	visitorsRateLimit sync.Map // one rate limiter per user per service
}

// NewServerPool creates a new server pool
func NewServerPool() *ServerPool {
	return &ServerPool{
		backends:          make(map[string]*Backend),
		current:           0,
		visitorsRateLimit: sync.Map{},
	}
}

func loadConfig(configPath string, consulClient *api.Client) (*http.ServeMux, *Config, error) {
	log.Printf("Loading configuration from %s...", configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("could not parse config YAML: %w", err)
	}

	router := buildRouter(&cfg, consulClient)
	log.Println("Configuration loaded successfully.")
	return router, &cfg, nil
}

func watchConfig(configPath string, globalRouter *atomic.Value, consulClient *api.Client) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create file watcher: %v", err)
	}
	configDir := filepath.Dir(configPath)
	configName := filepath.Base(configPath)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) == configName {
					if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
						log.Println("Config file modified. Reloading...")
						time.Sleep(100 * time.Millisecond)
						newRouter, _, err := loadConfig(configPath, consulClient)
						if err != nil {
							log.Printf("Error reloading config: %v. Keeping old config.", err)
							continue
						}

						globalRouter.Store(newRouter)
						log.Println("Hot reload complete. New configuration is active.")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("File watcher error:", err)
			}
		}
	}()

	err = watcher.Add(configDir)
	if err != nil {
		log.Fatalf("Failed to add config directory to watcher: %v", err)
	}
	log.Printf("Watching for config changes in directory: %s", configDir)
}

func (s *ServerPool) RemoveBackend(serviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if b, ok := s.backends[serviceID]; ok {
		delete(s.backends, serviceID)
		log.Printf("Removed backend: %s (ID: %s)", b.URL, serviceID)
	}
}

// AddBackend adds a new backend server to the pool
func (s *ServerPool) AddBackend(serviceID string, backendURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.backends[serviceID]; ok {
		return fmt.Errorf("backend with ID %s already exists", serviceID)
	}

	parsedURL, err := url.Parse(backendURL)
	if err != nil {
		return err
	}

	backend := &Backend{
		URL: parsedURL,
	}
	proxy := httputil.NewSingleHostReverseProxy(parsedURL)

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("Backend error: %v", e)
		backend.SetAlive(false)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
	}

	backend.SetAlive(true)
	backend.ReverseProxy = proxy
	s.backends[serviceID] = backend
	log.Printf("Added backend: %s, id: %s", backendURL, serviceID)
	return nil
}

// GetNextBackend atomically increments the counter and returns the next backend
func (s *ServerPool) GetNextBackend() *Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totalBackends := len(s.backends)
	if totalBackends == 0 {
		return nil
	}

	ids := make([]string, 0, totalBackends)
	for id := range s.backends {
		ids = append(ids, id)
	}
	nextIndex := atomic.AddUint64(&s.current, 1)
	for i := 0; i < totalBackends; i++ {
		idx := (nextIndex + uint64(i)) % uint64(totalBackends)
		backend := s.backends[ids[idx]]

		if backend.isAlive.Load() {
			return backend
		}
	}
	return nil
}

func (b *Backend) SetAlive(alive bool) {
	b.isAlive.Store(alive)
}

func newServiceHandler(pool *ServerPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backend := pool.GetNextBackend()
		if backend == nil {
			log.Println("No healthy backends for this service!")
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		log.Printf("Forwarding request to: %s", backend.URL)
		backend.ReverseProxy.ServeHTTP(w, r)
	}
}

func buildRouter(cfg *Config, consulClient *api.Client) *http.ServeMux {
	log.Println("Building new router...")
	mux := http.NewServeMux()
	var rsaPubKey *rsa.PublicKey
	if cfg.Authentication.Enabled {
		var err error
		rsaPubKey, err = loadPublicKey(cfg.Authentication.PublicKeyPath)
		if err != nil {
			log.Fatalf("Failed to load public key: %v. Server cannot start.", err)
		}
		log.Println("Successfully loaded RSA public key for JWT validation.")
	}

	for _, service := range cfg.Services {
		if service.ConsulServiceName == "" {
			log.Printf("Skipping service '%s': missing 'consulServiceName'", service.Name)
			continue
		}

		pool := NewServerPool()
		pool.startConsulWatcher(consulClient, service.ConsulServiceName)

		// --- MIDDLEWARE CHAINING ---
		var handler http.Handler = newServiceHandler(pool)
		if cfg.RateLimiting.Enabled {
			log.Printf("Enabling rate limiting for service '%s'", service.Name)
			handler = rateLimitMiddleware(handler, cfg.RateLimiting, pool)
			pool.startVisitorsRateLimitJanitor()
		}
		if cfg.Authentication.Enabled {
			log.Printf("Enabling JWT authentication for service '%s'", service.Name)
			handler = jwtAuthMiddleware(handler, rsaPubKey)
		}
		handler = metricsMiddleware(handler, service.Name)
		mux.Handle(service.Path, handler)
		log.Printf("Registered handler for service '%s' at path '%s'", service.Name, service.Path)
	}
	return mux
}

func main() {
	configPath := "./config/config.yaml"
	consulClient, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		log.Fatalf("Failed to create Consul client: %v", err)
	}

	initialRouter, cfg, err := loadConfig(configPath, consulClient)
	if err != nil {
		log.Fatalf("Failed to load initial configuration: %v", err)
	}

	var globalRouter atomic.Value
	globalRouter.Store(initialRouter)

	go watchConfig(configPath, &globalRouter, consulClient)

	proxyRootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router := globalRouter.Load().(*http.ServeMux)
		router.ServeHTTP(w, r)
	})

	mainRouter := http.NewServeMux()
	mainRouter.Handle("/metrics", promhttp.Handler())
	mainRouter.Handle("/", proxyRootHandler)

	if cfg.TLS.Enabled {
		go func() {
			httpPort := ":" + cfg.GatewayPort
			log.Printf("Starting HTTP-to-HTTPS redirect server on port %s", cfg.GatewayPort)
			redirectMux := http.NewServeMux()
			redirectMux.HandleFunc("/", createRedirectHandler(cfg.TLS.HTTPSPort))
			if err := http.ListenAndServe(httpPort, redirectMux); err != nil {
				// Don't use Fatalf here, as the main HTTPS server is the important one
				log.Printf("Redirect server failed: %v", err)
			}
		}()

		httpsPort := ":" + cfg.TLS.HTTPSPort
		log.Printf("API Gateway (HTTPS) listening on port %s", cfg.TLS.HTTPSPort)
		if err := http.ListenAndServeTLS(httpsPort, cfg.TLS.CertFile, cfg.TLS.KeyFile, mainRouter); err != nil {
			log.Fatalf("Gateway server (HTTPS) failed: %v", err)
		}
	} else {
		log.Printf("API Gateway listening on port %s", cfg.GatewayPort)
		if err := http.ListenAndServe(":"+cfg.GatewayPort, mainRouter); err != nil {
			log.Fatalf("Gateway server failed: %v", err)
		}
	}
}
