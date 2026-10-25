package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/samwilson0745/live-score/internal/cache"
	"github.com/samwilson0745/live-score/internal/provider/sportscore"
)

type MatchesHandler struct {
	provider       *sportscore.Client
	cache          *cache.RedisCache
	redisAvailable bool
}

func NewMatchesHandler(
	provider *sportscore.Client,
	redisCache *cache.RedisCache,
	redisAvailable bool,
) *MatchesHandler {
	return &MatchesHandler{
		provider:       provider,
		cache:          redisCache,
		redisAvailable: redisAvailable,
	}
}

func (h *MatchesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	if h.redisAvailable {
		cached, err := h.cache.Get(r.Context(), key, &result)
		if err != nil {
			h.cache.LogError("get", err)
		}

		if cached {
			log.Printf("cache=HIT key=%s", key)
			w.Header().Set("X-Cache", "HIT")
			writeJSON(w, http.StatusOK, result)
			return
		}
	}

	log.Printf("cache=MISS key=%s", key)
	log.Printf("provider=sportscore operation=get_matches sport=%s", sport)

	fetched, err := h.provider.GetMatches(r.Context(), sport, limit)
	if err != nil {
		log.Printf("get matches failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "sports data provider unavailable",
		})
		return
	}

	result = *fetched

	if h.redisAvailable {
		if err := h.cache.Set(r.Context(), key, result); err != nil {
			h.cache.LogError("set", err)
		}
	}

	if h.redisAvailable {
		w.Header().Set("X-Cache", "MISS")
	} else {
		w.Header().Set("X-Cache", "BYPASS")
	}

	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("write JSON response failed: %v", err)
	}
}
