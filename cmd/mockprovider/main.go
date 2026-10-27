// Command mockprovider imitates the SportScore widget API for load tests, so
// they never spend the real provider's quota. Behavior is tunable:
//
//	go run ./cmd/mockprovider -addr :9090 -delay 200ms -fail 0.1
//
// Point the API at it with SPORTSCORE_BASE_URL=http://localhost:9090.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	delay := flag.Duration("delay", 0, "artificial latency per request")
	fail := flag.Float64("fail", 0, "fraction of requests answered with HTTP 500")
	evolve := flag.Bool("evolve", false, "make every match live and raise its score on each request")
	flag.Parse()

	var calls atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/widget/matches/", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		time.Sleep(*delay)
		if rand.Float64() < *fail {
			http.Error(w, "mock failure", http.StatusInternalServerError)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 100 {
			limit = 5
		}
		sport := r.URL.Query().Get("sport")
		matches := make([]map[string]any, 0, limit)
		for i := 0; i < limit; i++ {
			var hs, as any
			status, text := "upcoming", "Not started"
			if *evolve {
				hs, as, status, text = strconv.Itoa(int(n)+i), strconv.Itoa(int(n)/2), "inprogress", "2nd half"
			} else if i%2 == 0 {
				hs, as, status, text = strconv.Itoa(i%4), strconv.Itoa(i%3), "finished", "Finished"
			}
			matches = append(matches, map[string]any{
				"home": fmt.Sprintf("Home %d", i), "away": fmt.Sprintf("Away %d", i),
				"home_score": hs, "away_score": as, "status": status, "status_text": text,
				"time": time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339), "competition": "Mock League",
				"url": fmt.Sprintf("/%s/match/home-%d-vs-away-%d/mock%d/", sport, i, i, i),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sport": sport, "count": len(matches), "matches": matches, "updated": time.Now().UTC().Format(time.RFC3339Nano),
		})
	})
	mux.HandleFunc("GET /calls", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, calls.Load()) })

	log.Printf("mock provider on %s (delay=%s fail=%.2f)", *addr, *delay, *fail)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
