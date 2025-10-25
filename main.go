package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

// Backend represents a single upstream server
type Backend struct {
	URL          *url.URL
	ReverseProxy *httputil.ReverseProxy
}

// ServerPool holds the list of available backends
type ServerPool struct {
	backends []*Backend
	current  uint64
}

// AddBackend adds a new backend server to the pool
func (s *ServerPool) AddBackend(backendURL string) error {
	parsedURL, err := url.Parse(backendURL)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(parsedURL)

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("Backend error: %v", e)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
	}

	s.backends = append(s.backends, &Backend{
		URL:          parsedURL,
		ReverseProxy: proxy,
	})
	log.Printf("Added backend: %s", backendURL)
	return nil
}

// GetNextBackend atomically increments the counter and returns the next backend
func (s *ServerPool) GetNextBackend() *Backend {
	nextIndex := atomic.AddUint64(&s.current, 1)
	return s.backends[nextIndex%uint64(len(s.backends))]
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
			log.Fatalf("Could not add backend: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", lb(pool))

	log.Printf("API Gateway listening on port %s", gatewayPort)
	if err := http.ListenAndServe(":"+gatewayPort, mux); err != nil {
		log.Fatalf("Gateway server failed: %v", err)
	}
}
