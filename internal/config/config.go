// Package config loads settings from environment variables.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
)

type Config struct {
	HTTPAddr        string
	RedisAddr       string // empty disables caching (set REDIS_ADDR=disabled)
	CacheTTL        time.Duration
	DatabaseURL     string // empty => no persistence
	SupportedSports []domain.Sport
	ProviderBaseURL string
	ProviderTimeout time.Duration
	MaxProviderConc int
	WSOrigins       []string

	IngestEnabled  bool // run the ingest worker inside the API process
	IngestInterval time.Duration
	IngestLimit    int
	IngestSports   []domain.Sport
}

// Load reads configuration using getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	var errs []string
	str := func(key, def string) string {
		if v, ok := lookup(getenv, key); ok {
			return v
		}
		return def
	}
	dur := func(key string, def time.Duration) time.Duration {
		v, ok := lookup(getenv, key)
		if !ok || v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, key+" must be a positive duration")
			return def
		}
		return d
	}
	num := func(key string, def int) int {
		v, ok := lookup(getenv, key)
		if !ok || v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, key+" must be a positive integer")
			return def
		}
		return n
	}
	boolean := func(key string) bool {
		b, _ := strconv.ParseBool(str(key, ""))
		return b
	}

	c := Config{
		HTTPAddr:        str("HTTP_ADDR", ":8080"),
		RedisAddr:       str("REDIS_ADDR", "localhost:6379"),
		CacheTTL:        dur("CACHE_TTL", 30*time.Second),
		DatabaseURL:     str("DATABASE_URL", ""),
		SupportedSports: sports(str("SUPPORTED_SPORTS", "football,basketball,tennis,cricket")),
		ProviderBaseURL: str("SPORTSCORE_BASE_URL", ""),
		ProviderTimeout: dur("PROVIDER_TIMEOUT", 8*time.Second),
		MaxProviderConc: num("MAX_PROVIDER_CONCURRENCY", 16),
		WSOrigins:       split(str("WS_ALLOWED_ORIGINS", "")),
		IngestEnabled:   boolean("INGEST_ENABLED"),
		IngestInterval:  dur("INGEST_INTERVAL", 30*time.Second),
		IngestLimit:     num("INGEST_LIMIT", 50),
	}
	if strings.EqualFold(c.RedisAddr, "disabled") {
		c.RedisAddr = ""
	}
	if v := str("INGEST_SPORTS", ""); v != "" {
		c.IngestSports = sports(v)
	} else {
		c.IngestSports = c.SupportedSports
	}

	if len(c.SupportedSports) == 0 {
		errs = append(errs, "SUPPORTED_SPORTS must list at least one sport")
	}
	if c.IngestEnabled && c.DatabaseURL == "" {
		errs = append(errs, "INGEST_ENABLED requires DATABASE_URL")
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

func lookup(getenv func(string) string, key string) (string, bool) {
	v := strings.TrimSpace(getenv(key))
	return v, v != ""
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sports(s string) []domain.Sport {
	var out []domain.Sport
	for _, p := range split(s) {
		out = append(out, domain.Sport(strings.ToLower(p)))
	}
	return out
}
