package main

import (
	"golang.org/x/time/rate"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

func rateLimitMiddleware(next http.Handler, cfg RateLimitConfig, pool *ServerPool) http.Handler {
	limit := rate.Limit(cfg.RatePerSecond)
	burst := cfg.Burst

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			if !strings.Contains(r.RemoteAddr, ":") {
				ip = r.RemoteAddr
			} else {
				log.Printf("Could not parse IP: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}

		var v *visitor
		vInterface, exists := pool.visitorsRateLimit.Load(ip)

		if !exists {
			log.Printf("Creating new limiter for IP %s for pool", ip)
			limiter := rate.NewLimiter(limit, burst)
			v = &visitor{limiter: limiter, lastSeen: time.Now()}
			pool.visitorsRateLimit.Store(ip, v)
		} else {
			v = vInterface.(*visitor)
			v.lastSeen = time.Now()
		}

		if !v.limiter.Allow() {
			http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *ServerPool) cleanupVisitorsRateLimit() {
	log.Printf("Running visitor cleanup for pool...")
	s.visitorsRateLimit.Range(func(key, value interface{}) bool {
		v := value.(*visitor)
		// Evict if not seen in the last 5 minutes
		if time.Since(v.lastSeen) > 5*time.Minute {
			s.visitorsRateLimit.Delete(key)
			log.Printf("Evicted rate limiter for IP: %s", key.(string))
		}
		return true
	})
}

func (s *ServerPool) startVisitorsRateLimitJanitor() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for {
			<-ticker.C
			s.cleanupVisitorsRateLimit()
		}
	}()
}
