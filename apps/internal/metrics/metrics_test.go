package metrics

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/trace"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
)

func TestWrapAttachesTraceToExemplarAndLog(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	req := httptest.NewRequest("POST", "/ex", nil).WithContext(trace.ContextWithSpanContext(context.Background(), sc))
	Wrap("POST /ex", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)

	for _, want := range []string{`"msg":"request"`, `"code":200`, `"trace_id":"` + tid.String() + `"`, `"span_id":"` + sid.String() + `"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("access log %q missing %s", logs.String(), want)
		}
	}

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
	for _, code := range []string{"500", "200"} {
		var m dto.Metric
		if err := duration.WithLabelValues("POST /test", "POST", code).(prometheus.Metric).Write(&m); err != nil {
			t.Fatal(err)
		}
		if got := m.GetHistogram().GetSampleCount(); got != 1 {
			t.Fatalf("duration code %s: got %d observations, want 1", code, got)
		}
	}
}

func TestWrapCreatesErrorSeriesAtZero(t *testing.T) {
	Wrap("POST /fresh", http.NotFoundHandler())
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != "http_server_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			l := map[string]string{}
			for _, lp := range m.GetLabel() {
				l[lp.GetName()] = lp.GetValue()
			}
			if l["route"] == "POST /fresh" && l["method"] == "POST" {
				seen[l["code"]] = m.GetCounter().GetValue()
			}
		}
	}
	for _, code := range []string{"500", "502", "503"} {
		if v, ok := seen[code]; !ok || v != 0 {
			t.Fatalf("code %s: series present=%v value=%v, want present at 0", code, ok, v)
		}
	}
}
