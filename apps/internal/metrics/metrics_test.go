package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/trace"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
)

func TestWrapAttachesTraceExemplar(t *testing.T) {
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	req := httptest.NewRequest("POST", "/ex", nil).WithContext(trace.ContextWithSpanContext(context.Background(), sc))
	Wrap("POST /ex", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "http_server_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, b := range m.GetHistogram().GetBucket() {
				for _, l := range b.GetExemplar().GetLabel() {
					if l.GetName() == "trace_id" && l.GetValue() == tid.String() {
						return
					}
				}
			}
		}
	}
	t.Fatal("no histogram exemplar with the request's trace_id")
}

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
