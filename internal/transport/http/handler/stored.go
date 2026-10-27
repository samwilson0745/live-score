package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
)

// StoredHandler serves endpoints that need the persistent repository.
type StoredHandler struct {
	repo ports.MatchRepository
	log  *slog.Logger
}

func NewStoredHandler(repo ports.MatchRepository, log *slog.Logger) *StoredHandler {
	if log == nil {
		log = slog.Default()
	}
	return &StoredHandler{repo: repo, log: log}
}

func (h *StoredHandler) GetMatch(w http.ResponseWriter, r *http.Request) {
	m, err := h.repo.GetMatch(r.Context(), r.PathValue("id"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "match not found")
		return
	}
	if err != nil {
		h.log.Error("get match failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *StoredHandler) CompetitionMatches(w http.ResponseWriter, r *http.Request) {
	limit := DefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and "+strconv.Itoa(MaxLimit))
			return
		}
		limit = n
	}
	ms, err := h.repo.ListMatches(r.Context(), ports.MatchFilter{CompetitionID: r.PathValue("id"), Limit: limit})
	if err != nil {
		h.log.Error("list competition matches failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, domain.NewMatchesResult("", ms, latest(ms)))
}

func latest(ms []domain.Match) (t time.Time) {
	for _, m := range ms {
		if m.ProviderUpdatedAt.After(t) {
			t = m.ProviderUpdatedAt
		}
	}
	return t
}
