package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Backend represents a single upstream server
type Backend struct {
	URL          *url.URL
	ReverseProxy *httputil.ReverseProxy
	isAlive      atomic.Bool
}

// ServerPool holds the list of available backends
type ServerPool struct {
	backends []*Backend
	current  uint64
	mu       sync.RWMutex
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

func lb(pool *ServerPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request for: %s", r.URL.Path)

		backend := pool.GetNextBackend()

		log.Printf("Forwarding request to: %s", backend.URL)

		backend.ReverseProxy.ServeHTTP(w, r)
	}
}

func main() {
	backendServers := []string{
		"http://localhost:8081",
		"http://localhost:8082",
		"http://localhost:8083",
	}

	gatewayPort := "8000"
	pool := &ServerPool{current: 0}

	for _, serverURL := range backendServers {
		if err := pool.AddBackend(serverURL); err != nil {
			log.Printf("Could not add backend: %v", err)
		}
	}

	healthCheckInterval := 5 * time.Second
	pool.StartHealthChecks(healthCheckInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("/", lb(pool))

	log.Printf("API Gateway listening on port %s", gatewayPort)
	if err := http.ListenAndServe(":"+gatewayPort, mux); err != nil {
		log.Fatalf("Gateway server failed: %v", err)
	}
}
