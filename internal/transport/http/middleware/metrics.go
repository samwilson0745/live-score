package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/samwilson0745/live-score/internal/metrics"
)

// Metrics records request count and latency labelled by route pattern, which
// keeps cardinality bounded (never the raw path).
func Metrics(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			route := r.Pattern // set by ServeMux on the request during dispatch
			if route == "" {
				route = "unmatched"
			}
			m.ObserveHTTP(route, r.Method, strconv.Itoa(rec.code()), time.Since(start))
		})
	}
}
