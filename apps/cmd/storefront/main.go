package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alexboscolo954/sre-reference-platform/apps/internal/fault"
)

func main() {
	checkout := os.Getenv("CHECKOUT_URL") // e.g. http://checkout.shop.svc
	client := &http.Client{Timeout: 5 * time.Second}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "storefront: POST /buy to place an order\n")
	})
	mux.HandleFunc("POST /buy", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.Post(checkout+"/checkout", "application/json", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	log.Fatal(http.ListenAndServe(":8080", fault.Middleware(mux)))
}
