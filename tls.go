package main

import (
	"log"
	"net"
	"net/http"
	"net/url"
)

type TLSConfig struct {
	Enabled   bool   `yaml:"enabled"`
	HTTPSPort string `yaml:"httpsPort"`
	CertFile  string `yaml:"certFile"`
	KeyFile   string `yaml:"keyFile"`
}

// createRedirectHandler creates a handler to redirect HTTP to HTTPS
// when TLS is enabled, we enforced user to use HTTPS connection
func createRedirectHandler(httpsPort string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}

		targetURL := url.URL{
			Scheme:   "https",
			Host:     net.JoinHostPort(host, httpsPort),
			Path:     r.URL.Path,
			RawQuery: r.URL.Query().Encode(),
		}

		log.Printf("Redirecting HTTP request from %s to %s", r.RemoteAddr, targetURL.String())
		http.Redirect(w, r, targetURL.String(), http.StatusMovedPermanently) // 301
	}
}
