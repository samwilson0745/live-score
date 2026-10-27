package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/storage/contract"
	"github.com/samwilson0745/live-score/internal/storage/postgres"
	"github.com/samwilson0745/live-score/migrations"
)

// Integration tests need a disposable database:
//
//	TEST_DATABASE_URL=postgres://live:live@localhost:5432/live_test go test ./internal/storage/postgres
//
// Every table is dropped and recreated, so never point this at real data.
func store(t *testing.T) *postgres.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRepositoryContract(t *testing.T) {
	store(t) // skip early when no database is configured
	contract.Run(t, func(t *testing.T) ports.MatchRepository { return store(t) })
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := store(t)
	if err := s.Migrate(context.Background(), migrations.FS); err != nil {
		t.Fatal(err)
	}
}

func TestSyncState(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	if _, err := s.GetSyncState(ctx, "p", "football"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	_ = s.RecordSync(ctx, ports.SyncRecord{Provider: "p", Scope: "football", Attempted: at, Success: true, Duration: time.Second})
	_ = s.RecordSync(ctx, ports.SyncRecord{Provider: "p", Scope: "football", Attempted: at.Add(time.Minute), Err: "boom"})
	st, err := s.GetSyncState(ctx, "p", "football")
	if err != nil || st.ConsecutiveFailures != 1 || st.LastError != "boom" || st.LastSuccess.IsZero() {
		t.Fatalf("state = %+v err=%v", st, err)
	}
}
