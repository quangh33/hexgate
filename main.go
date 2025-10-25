package main

import (
	"crypto/rsa"
	"fmt"
	"github.com/fsnotify/fsnotify"
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
	GatewayPort         string          `yaml:"gatewayPort"`
	HealthCheckInterval int64           `yaml:"healthCheckInterval"`
	Services            []Service       `yaml:"services"`
	RateLimiting        RateLimitConfig `yaml:"rateLimiting"`
	Authentication      AuthConfig      `yaml:"authentication"`
}

type RateLimitConfig struct {
	Enabled       bool    `yaml:"enabled"`
	RatePerSecond float64 `yaml:"ratePerSecond"`
	Burst         int     `yaml:"burst"`
}

type Service struct {
	Name     string   `yaml:"name"`
	Backends []string `yaml:"backends"`
	Path     string   `yaml:"path"`
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
	backends          []*Backend
	current           uint64
	mu                sync.RWMutex
	visitorsRateLimit sync.Map // one rate limiter per user per service
}

// NewServerPool creates a new server pool
func NewServerPool() *ServerPool {
	return &ServerPool{
		backends:          make([]*Backend, 0),
		current:           0,
		visitorsRateLimit: sync.Map{},
	}
}

func loadConfig(configPath string) (*http.ServeMux, *Config, error) {
	log.Printf("Loading configuration from %s...", configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("could not parse config YAML: %w", err)
	}

	router := buildRouter(&cfg)
	log.Println("Configuration loaded successfully.")
	return router, &cfg, nil
}

func watchConfig(configPath string, globalRouter *atomic.Value) {
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
						newRouter, _, err := loadConfig(configPath)
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

// AddBackend adds a new backend server to the pool
func (s *ServerPool) AddBackend(backendURL string) error {
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
	s.mu.Lock()
	s.backends = append(s.backends, backend)
	s.mu.Unlock()
	log.Printf("Added backend: %s", backendURL)
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
	nextIndex := atomic.AddUint64(&s.current, 1)
	for i := 0; i < totalBackends; i++ {
		idx := (nextIndex + uint64(i)) % uint64(totalBackends)
		backend := s.backends[idx]

		if backend.isAlive.Load() {
			return backend
		}
	}
	return nil
}

func (b *Backend) SetAlive(alive bool) {
	currentStatus := b.isAlive.Load()

	if currentStatus != alive {
		b.isAlive.Store(alive)
		if alive {
			log.Printf("Backend %s is now HEALTHY", b.URL)
		} else {
			log.Printf("Backend %s is now UNHEALTHY", b.URL)
		}
	}
}

func (s *ServerPool) StartHealthChecks(interval time.Duration) {
	healthCheckClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	log.Println("Starting health checks...")

	go func() {
		for {
			s.mu.RLock()
			backends := make([]*Backend, len(s.backends))
			copy(backends, s.backends)
			s.mu.RUnlock()

			var wg sync.WaitGroup
			for _, b := range backends {
				wg.Add(1)
				go func(backend *Backend) {
					defer wg.Done()
					req, err := http.NewRequest("HEAD", backend.URL.String()+"/health", nil)
					if err != nil {
						log.Printf("Error creating health check request for %s: %v", backend.URL, err)
						return
					}
					resp, err := healthCheckClient.Do(req)
					if err != nil {
						backend.SetAlive(false)
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						backend.SetAlive(false)
					} else {
						backend.SetAlive(true)
					}
				}(b)
			}
			wg.Wait()
			time.Sleep(interval)
		}
	}()
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

func buildRouter(cfg *Config) *http.ServeMux {
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
		if len(service.Backends) == 0 {
			log.Printf("Skipping service '%s': no backends configured.", service.Name)
			continue
		}

		pool := NewServerPool()
		for _, backendURL := range service.Backends {
			if err := pool.AddBackend(backendURL); err != nil {
				log.Printf("Could not add backend %s for service %s: %v", backendURL, service.Name, err)
			}
		}

		pool.StartHealthChecks(time.Duration(cfg.HealthCheckInterval) * time.Second)

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
		mux.Handle(service.Path, handler)
		log.Printf("Registered handler for service '%s' at path '%s'", service.Name, service.Path)
	}
	return mux
}

func main() {
	configPath := "./config/config.yaml"
	initialRouter, cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load initial configuration: %v", err)
	}

	var globalRouter atomic.Value
	globalRouter.Store(initialRouter)

	go watchConfig(configPath, &globalRouter)

	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router := globalRouter.Load().(*http.ServeMux)
		router.ServeHTTP(w, r)
	})

	log.Printf("API Gateway listening on port %s", cfg.GatewayPort)
	if err := http.ListenAndServe(":"+cfg.GatewayPort, rootHandler); err != nil {
		log.Fatalf("Gateway server failed: %v", err)
	}
}
