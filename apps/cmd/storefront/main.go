package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
	"github.com/alexboscolo954/sre-reference-platform/apps/internal/metrics"
	"github.com/alexboscolo954/sre-reference-platform/apps/internal/tracing"
)

func main() {
	checkout := os.Getenv("CHECKOUT_URL") // e.g. http://checkout.shop.svc
	tracing.Init(context.Background(), "storefront")
	client := &http.Client{Timeout: 5 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "storefront: POST /buy to place an order\n")
	})
	mux.Handle("POST /buy", otelhttp.NewHandler(metrics.Wrap("POST /buy", fault.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Forward the body (optional {"email": ...}) and context so the trace continues in checkout.
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, checkout+"/checkout", r.Body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))), "POST /buy"))

	metrics.Serve(":9090")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
