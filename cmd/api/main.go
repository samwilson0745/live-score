package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"context"
	"time"

	"github.com/samwilson0745/live-score/internal/cache"
	"github.com/samwilson0745/live-score/internal/provider/sportscore"
)

func main() {
	provider := sportscore.NewClient()
	mux := http.NewServeMux()

	redisCache := cache.NewRedisCache(
		"localhost:6379",
		30*time.Second,
	)

	defer redisCache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := redisCache.Ping(ctx); err != nil {
		log.Printf("Redis unavailable; caching will be bypassed: %v", err)
	} else {
		log.Println("Redis connected")
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "live-score",
		})
	})

	mux.HandleFunc("GET /api/v1/matches", func(w http.ResponseWriter, r *http.Request) {
		sport := r.URL.Query().Get("sport")
		if sport == "" {
			sport = "football"
		}

		limit := 5
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "limit must be between 1 and 100",
				})
				return
			}
			limit = parsed
		}

		key := fmt.Sprintf("matches:%s:%d", sport, limit)

		var result sportscore.MatchesResponse

		// 1. Try Redis first.
		cached, err := redisCache.Get(r.Context(), key, &result)
		if err != nil {
			redisCache.LogError("get", err)
		}

		if cached {
			w.Header().Set("X-Cache", "HIT")
			writeJSON(w, http.StatusOK, result)
			return
		}

		// 2. Cache miss: fetch from SportScore.
		fetched, err := provider.GetMatches(r.Context(), sport, limit)
		if err != nil {
			log.Printf("get matches failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "sports data provider unavailable",
			})
			return
		}

		result = *fetched

		// 3. Cache the response. Cache failure shouldn't fail the request.
		if err := redisCache.Set(r.Context(), key, result); err != nil {
			redisCache.LogError("set", err)
		}

		w.Header().Set("X-Cache", "MISS")
		writeJSON(w, http.StatusOK, result)
	})

	log.Println("live-score server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write JSON response failed: %v", err)
	}
}
