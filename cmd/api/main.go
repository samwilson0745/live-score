package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/samwilson0745/live-score/internal/cache"
	"github.com/samwilson0745/live-score/internal/provider/sportscore"
	"github.com/samwilson0745/live-score/internal/transport/http/handler"
	"github.com/samwilson0745/live-score/internal/transport/http/middleware"
)

func main() {
	provider := sportscore.NewClient()
	ctx, cancel := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancel()
	redisCache := cache.NewRedisCache(
		"localhost:6379",
		30*time.Second,
	)
	defer redisCache.Close()

	// Check Redis connectivity at startup.
	redisAvailable := false

	if err := redisCache.Ping(ctx); err != nil {
		log.Printf("Redis unavailable; caching will be bypassed: %v", err)
	} else {
		redisAvailable = true
		log.Println("Redis connected")
	}

	mux := http.NewServeMux()

	// Health endpoint.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "live-score",
		})
	})

	// List matches.
	matchesHandler := handler.NewMatchesHandler(
		provider,
		redisCache,
		redisAvailable,
	)

	mux.Handle("GET /api/v1/matches", matchesHandler)

	server := &http.Server{
		Addr:         ":8080",
		Handler:      middleware.RequestLogger(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Println("live-score server starting on :8080")
	log.Fatal(server.ListenAndServe())
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write JSON response failed: %v", err)
	}
}
