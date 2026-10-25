package sportscore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const baseURL = "https://sportscore.com"

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) GetMatches(
	ctx context.Context,
	sport string,
	limit int,
) (*MatchesResponse, error) {
	if sport == "" {
		return nil, fmt.Errorf("sport is required")
	}

	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}

	endpoint, err := url.Parse(baseURL + "/api/widget/matches/")
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}

	query := endpoint.Query()
	query.Set("sport", sport)
	query.Set("limit", fmt.Sprint(limit))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch matches from SportScore: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SportScore returned HTTP %d", resp.StatusCode)
	}

	var result MatchesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode SportScore response: %w", err)
	}

	return &result, nil
}
