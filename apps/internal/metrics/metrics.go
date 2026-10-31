// Package metrics records RED metrics for HTTP handlers and serves /metrics
// on its own port, so probes and scrapes never count toward the SLIs.
package metrics

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_server_requests_total",
		Help: "HTTP requests by route, method and status code.",
	}, []string{"route", "method", "code"})

	// 0.3 is the checkout latency SLO threshold.
	duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_request_duration_seconds",
		Help:    "HTTP request duration by route and method.",
		Buckets: []float64{0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1, 2.5, 5},
	}, []string{"route", "method"})
)

type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Wrap counts and times h under the given route label. Put it outside
// fault.Middleware so injected errors and latency are measured.
func Wrap(route string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		h.ServeHTTP(rec, r)
		requests.WithLabelValues(route, r.Method, strconv.Itoa(rec.code)).Inc()
		obs := duration.WithLabelValues(route, r.Method)
		secs := time.Since(start).Seconds()
		// Needs otelhttp outside Wrap so the request context carries the span.
		if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
			obs.(prometheus.ExemplarObserver).ObserveWithExemplar(secs, prometheus.Labels{"trace_id": sc.TraceID().String()})
			return
		}
		obs.Observe(secs)
	})
}

// Serve exposes /metrics on addr in the background.
func Serve(addr string) {
	mux := http.NewServeMux()
	// OpenMetrics is required for Prometheus to ingest exemplars.
	mux.Handle("GET /metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	go func() { log.Fatal(http.ListenAndServe(addr, mux)) }()
}
