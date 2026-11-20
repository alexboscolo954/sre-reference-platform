package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/valkeyotel"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
	"github.com/alexboscolo954/sre-reference-platform/apps/internal/metrics"
	"github.com/alexboscolo954/sre-reference-platform/apps/internal/tracing"
)

func main() {
	ctx := context.Background()
	// JSON to stdout for the log agent; log.Fatal output goes through it too.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	tracing.Init(ctx, "checkout")

	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL")) // CNPG's shop-db-app secret "uri"
	if err != nil {
		log.Fatal(err)
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	// ForceSingleClient returns a client even when Valkey is down and redials later (GameDay 0001):
	// valkeyotel.NewClient would drop it, so the tracing wrapper is added afterwards.
	vkc, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{os.Getenv("VALKEY_ADDR")}, ForceSingleClient: true})
	if vkc == nil {
		log.Fatal(err)
	}
	vk := valkeyotel.WithClient(vkc)

	// Retried in the background so a missing primary does not crash-loop checkout (GameDay 0001).
	var schemaReady atomic.Bool
	go func() {
		for {
			_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS orders (id bigserial PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now())`)
			if err == nil {
				schemaReady.Store(true)
				return
			}
			slog.Warn("create orders table", "err", err)
			time.Sleep(2 * time.Second)
		}
	}()

	mux := http.NewServeMux()
	// Liveness: the process answers. Dependencies stay out of it, or their outage restarts checkout.
	mux.HandleFunc("GET /livez", func(http.ResponseWriter, *http.Request) {})
	// Readiness: Postgres only. Valkey holds a counter cache, so checkout serves without it.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !schemaReady.Load() {
			http.Error(w, "orders table not created yet", http.StatusServiceUnavailable)
			return
		}
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		}
	})
	mux.Handle("POST /checkout", otelhttp.NewHandler(metrics.Wrap("POST /checkout", fault.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Optional {"email": ...}. Recorded as-is: the collector hashes it, which is the point of the redaction demo.
		var in struct{ Email string }
		json.NewDecoder(r.Body).Decode(&in)
		if in.Email != "" {
			trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("user.email", in.Email))
		}
		var id int64
		// Bounded, so an unreachable database fails fast instead of at storefront's 5s timeout (GameDay 0002).
		ictx, icancel := context.WithTimeout(r.Context(), time.Second)
		defer icancel()
		if err := db.QueryRow(ictx, `INSERT INTO orders DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := map[string]int64{"order_id": id}
		// The order is already in Postgres; a Valkey outage only loses the counter. Bounded, because a
		// dial to a dead node otherwise hangs past storefront's 5s timeout and a saved order is answered 502 (GameDay 0001).
		vctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
		defer cancel()
		if total, err := vk.Do(vctx, vk.B().Incr().Key("orders_total").Build()).AsInt64(); err != nil {
			slog.WarnContext(r.Context(), "orders_total not incremented", "err", err)
		} else {
			out["orders_total"] = total
		}
		json.NewEncoder(w).Encode(out)
	}))), "POST /checkout"))

	metrics.Serve(":9090")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
