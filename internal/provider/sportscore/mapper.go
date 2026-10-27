package sportscore

import (
	"strconv"
	"strings"

	"github.com/samwilson0745/live-score/internal/domain"
)

// ToDomain converts a SportScore payload into canonical models.
func ToDomain(sport domain.Sport, r *MatchesResponse) *domain.MatchesResult {
	matches := make([]domain.Match, 0, len(r.Matches))
	for _, m := range r.Matches {
		matches = append(matches, mapMatch(sport, m, r))
	}
	return domain.NewMatchesResult(sport, matches, r.Updated)
}

func mapMatch(sport domain.Sport, m Match, r *MatchesResponse) domain.Match {
	providerID := providerMatchID(m.URL)
	if providerID == "" { // no usable URL: fall back to a content-derived reference
		providerID = domain.NewMatchID(ProviderName, m.Home+"|"+m.Away+"|"+m.Time.UTC().String())
	}
	return domain.Match{
		ID:    domain.NewMatchID(ProviderName, providerID),
		Sport: sport,
		Competition: domain.Competition{
			ID:      domain.NewCompetitionID(sport, m.Competition),
			Name:    m.Competition,
			LogoURL: m.CompetitionLogo,
		},
		Home:      domain.Team{ID: domain.NewTeamID(sport, m.Home), Name: m.Home, LogoURL: m.HomeLogo},
		Away:      domain.Team{ID: domain.NewTeamID(sport, m.Away), Name: m.Away, LogoURL: m.AwayLogo},
		HomeScore: parseScore(m.HomeScore),
		AwayScore: parseScore(m.AwayScore),
		Status:    NormalizeStatus(m.Status, m.StatusText),
		StartTime: m.Time.UTC(),
		Source: domain.ProviderRef{
			Provider:   ProviderName,
			MatchID:    providerID,
			URL:        m.URL,
			Status:     m.Status,
			StatusText: m.StatusText,
		},
		ProviderUpdatedAt: r.Updated.UTC(),
	}
}

// NormalizeStatus maps SportScore wording onto canonical statuses. The text
// is consulted first because "upcoming" is also used for postponed games.
func NormalizeStatus(status, text string) domain.MatchStatus {
	t := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(t, "postpone"):
		return domain.StatusPostponed
	case strings.Contains(t, "cancel"), strings.Contains(t, "abandon"):
		return domain.StatusCancelled
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "finished", "ended", "full_time":
		return domain.StatusFinished
	case "live", "inplay", "in_play", "inprogress", "in_progress":
		return domain.StatusLive
	case "upcoming", "scheduled", "notstarted", "not_started":
		return domain.StatusScheduled
	}
	return domain.StatusUnknown
}

// parseScore keeps "no score" (nil) distinct from a score of zero.
func parseScore(s *string) *int {
	if s == nil {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(*s))
	if err != nil {
		return nil
	}
	return &n
}

// providerMatchID extracts the trailing path segment of a match URL, e.g.
// "/football/match/a-vs-b/1l4rjnh99zlnm7v/" -> "1l4rjnh99zlnm7v".
func providerMatchID(u string) string {
	trimmed := strings.Trim(u, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}
