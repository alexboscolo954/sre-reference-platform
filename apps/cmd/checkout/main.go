package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

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
	vk, err := valkeyotel.NewClient(valkey.ClientOption{InitAddress: []string{os.Getenv("VALKEY_ADDR")}})
	if err != nil {
		log.Fatal(err)
	}

	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS orders (id bigserial PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if err := vk.Do(r.Context(), vk.B().Ping().Build()).Error(); err != nil {
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
		if err := db.QueryRow(r.Context(), `INSERT INTO orders DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		total, err := vk.Do(r.Context(), vk.B().Incr().Key("orders_total").Build()).AsInt64()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]int64{"order_id": id, "orders_total": total})
	}))), "POST /checkout"))

	metrics.Serve(":9090")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
