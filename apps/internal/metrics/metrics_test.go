package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
)

func TestWrapCountsInjectedErrors(t *testing.T) {
	t.Setenv("ERROR_RATE", "1")
	t.Setenv("LATENCY_MS", "0")
	h := Wrap("POST /test", fault.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/test", nil))

	if got := testutil.ToFloat64(requests.WithLabelValues("POST /test", "POST", "500")); got != 1 {
		t.Fatalf("500 count: got %v, want 1", got)
	}

	t.Setenv("ERROR_RATE", "0")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/test", nil))
	if got := testutil.ToFloat64(requests.WithLabelValues("POST /test", "POST", "200")); got != 1 {
		t.Fatalf("200 count: got %v, want 1", got)
	}
}
