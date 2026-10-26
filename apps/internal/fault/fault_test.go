package fault

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func do(t *testing.T) (int, time.Duration) {
	t.Helper()
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	return rec.Code, time.Since(start)
}

func TestMiddleware(t *testing.T) {
	t.Setenv("ERROR_RATE", "0")
	t.Setenv("LATENCY_MS", "0")
	if code, _ := do(t); code != 200 {
		t.Fatalf("rate 0: got %d, want 200", code)
	}

	t.Setenv("ERROR_RATE", "1")
	if code, _ := do(t); code != 500 {
		t.Fatalf("rate 1: got %d, want 500", code)
	}

	t.Setenv("ERROR_RATE", "0")
	t.Setenv("LATENCY_MS", "50")
	if _, d := do(t); d < 50*time.Millisecond {
		t.Fatalf("latency: got %v, want >=50ms", d)
	}
}
