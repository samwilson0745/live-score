package sportscore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
)

const (
	ProviderName   = "sportscore"
	DefaultBaseURL = "https://sportscore.com"

	maxLimit        = 100
	maxResponseSize = 5 << 20
)

var sportPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

type Client struct {
	baseURL    string
	httpClient *http.Client
	now        func() time.Time
}

type Option func(*Client)

func WithBaseURL(u string) Option          { return func(c *Client) { c.baseURL = u } }
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return ProviderName }

func (c *Client) GetMatches(ctx context.Context, sport string, limit int) (*domain.MatchesResult, error) {
	if !sportPattern.MatchString(sport) {
		return nil, fmt.Errorf("invalid sport %q", sport)
	}
	if limit < 1 || limit > maxLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}

	endpoint, err := url.Parse(c.baseURL + "/api/widget/matches/")
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	q := endpoint.Query()
	q.Set("sport", sport)
	q.Set("limit", strconv.Itoa(limit))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "live-score/0.1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &ports.ProviderError{Provider: ProviderName, Retryable: ctx.Err() == nil, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &ports.ProviderError{
			Provider:   ProviderName,
			StatusCode: resp.StatusCode,
			Retryable:  resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.now()),
		}
	}

	var raw MatchesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&raw); err != nil {
		return nil, &ports.ProviderError{Provider: ProviderName, Err: fmt.Errorf("decode response: %w", err)}
	}
	return ToDomain(domain.Sport(sport), &raw), nil
}

func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
