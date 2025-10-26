package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"net/http"
	"strconv"
	"time"
)

var (
	// httpRequestsTotal is a Counter vector to count requests
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hexgate_http_requests_total",
			Help: "Total number of HTTP requests processed by HexGate.",
		},
		[]string{"service", "method", "code"},
	)

	// httpRequestDuration is a Histogram vector to observe request latencies
	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hexgate_http_request_duration_seconds",
			Help:    "Histogram of HTTP request latencies.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "method"},
	)
)

type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func newResponseWriterInterceptor(w http.ResponseWriter) *responseWriterInterceptor {
	// Default to 200 OK, as WriteHeader is not called for 200
	return &responseWriterInterceptor{w, http.StatusOK}
}

// WriteHeader captures the status code before writing it
func (rwi *responseWriterInterceptor) WriteHeader(code int) {
	rwi.statusCode = code
	rwi.ResponseWriter.WriteHeader(code)
}

func metricsMiddleware(next http.Handler, serviceName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		rwi := newResponseWriterInterceptor(w)

		next.ServeHTTP(rwi, r)

		duration := time.Since(startTime).Seconds()
		statusCodeStr := strconv.Itoa(rwi.statusCode)

		httpRequestsTotal.WithLabelValues(serviceName, r.Method, statusCodeStr).Inc()
		httpRequestDuration.WithLabelValues(serviceName, r.Method).Observe(duration)
	})
}
