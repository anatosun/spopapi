package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"spopapi/internal/apiproxy"
	"spopapi/internal/store"
)

func main() {
	listen := envOr("LISTEN", ":8080")
	redisAddr := envOr("REDIS_ADDR", "localhost:6379")
	dsn := os.Getenv("DATABASE_URL") // optional: enables the seed fallback
	clientID := os.Getenv("SPOTIFY_CLIENT_ID")
	clientSecret := os.Getenv("SPOTIFY_CLIENT_SECRET")
	upstream := envOr("SPOTIFY_UPSTREAM", "https://api.spotify.com")
	proxyToken := os.Getenv("PROXY_API_TOKEN")
	if proxyToken == "" {
		log.Fatal("PROXY_API_TOKEN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer rdb.Close()

	var ts apiproxy.Store
	if dsn != "" {
		st, err := store.New(ctx, dsn)
		if err != nil {
			log.Fatalf("postgres: %v", err)
		}
		defer st.Close()
		ts = st
	} else {
		log.Print("DATABASE_URL not set: dump-seed fallback disabled")
	}

	proxy := &apiproxy.Proxy{
		RDB:   rdb,
		Store: ts,
		Up: &apiproxy.SpotifyClient{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			APIBase:      upstream,
			HTTP:         &http.Client{Timeout: 30 * time.Second},
		},
		TTLs: apiproxy.DefaultTTLs(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", proxy.Health)
	mux.HandleFunc("GET /stats", proxy.Stats)
	mux.HandleFunc("GET /v1/{path...}", proxy.Handle)

	srv := &http.Server{
		Addr:              listen,
		Handler:           apiproxy.BearerAuth(proxyToken, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s (upstream %s)", listen, upstream)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
