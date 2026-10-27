package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.RedisAddr != "localhost:6379" || c.CacheTTL != 30*time.Second || c.DatabaseURL != "" {
		t.Errorf("defaults: %+v", c)
	}
	if len(c.SupportedSports) == 0 || c.SupportedSports[0] != "football" {
		t.Errorf("sports = %v", c.SupportedSports)
	}
	if len(c.IngestSports) != len(c.SupportedSports) {
		t.Error("ingest sports should default to supported sports")
	}
}

func TestOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"HTTP_ADDR": ":9000", "CACHE_TTL": "5s", "SUPPORTED_SPORTS": " Football , Tennis ",
		"DATABASE_URL": "postgres://x", "INGEST_ENABLED": "true", "INGEST_SPORTS": "football",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":9000" || c.CacheTTL != 5*time.Second || !c.IngestEnabled {
		t.Errorf("%+v", c)
	}
	if len(c.SupportedSports) != 2 || c.SupportedSports[1] != "tennis" || len(c.IngestSports) != 1 {
		t.Errorf("sports = %v / %v", c.SupportedSports, c.IngestSports)
	}
}

func TestInvalid(t *testing.T) {
	for name, e := range map[string]map[string]string{
		"bad duration":      {"CACHE_TTL": "soon"},
		"negative duration": {"PROVIDER_TIMEOUT": "-1s"},
		"bad int":           {"MAX_PROVIDER_CONCURRENCY": "lots"},
		"ingest without db": {"INGEST_ENABLED": "true"},
	} {
		if _, err := Load(env(e)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestRedisCanBeDisabled(t *testing.T) {
	c, err := Load(env(map[string]string{"REDIS_ADDR": "disabled"}))
	if err != nil || c.RedisAddr != "" {
		t.Fatalf("addr=%q err=%v", c.RedisAddr, err)
	}
}
