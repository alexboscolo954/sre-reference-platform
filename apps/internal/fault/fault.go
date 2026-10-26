// Package fault injects latency and errors into HTTP handlers from env vars,
// so SLO and chaos work has something to break.
package fault

import (
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Middleware reads LATENCY_MS and ERROR_RATE (0-1) on every request, so a
// `kubectl set env` takes effect as soon as the pod restarts.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ms, _ := strconv.Atoi(os.Getenv("LATENCY_MS")); ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		if rate, _ := strconv.ParseFloat(os.Getenv("ERROR_RATE"), 64); rand.Float64() < rate {
			http.Error(w, "injected error", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	})
}
