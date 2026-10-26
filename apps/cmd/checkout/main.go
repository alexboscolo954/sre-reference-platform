package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/valkey-io/valkey-go"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
)

func main() {
	ctx := context.Background()

	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL")) // CNPG's shop-db-app secret "uri"
	if err != nil {
		log.Fatal(err)
	}
	vk, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{os.Getenv("VALKEY_ADDR")}})
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
	mux.HandleFunc("POST /checkout", func(w http.ResponseWriter, r *http.Request) {
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
	})

	log.Fatal(http.ListenAndServe(":8080", fault.Middleware(mux)))
}
