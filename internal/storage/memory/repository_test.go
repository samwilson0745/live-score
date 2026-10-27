package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samwilson0745/live-score/internal/domain"
	"github.com/samwilson0745/live-score/internal/ports"
	"github.com/samwilson0745/live-score/internal/storage/contract"
	"github.com/samwilson0745/live-score/internal/storage/memory"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestRepositoryContract(t *testing.T) {
	contract.Run(t, func(t *testing.T) ports.MatchRepository { return memory.New() })
}

func TestSyncState(t *testing.T) {
	r := memory.New()
	ctx := context.Background()
	if _, err := r.GetSyncState(ctx, "p", "football"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("want ErrNotFound")
	}
	_ = r.RecordSync(ctx, ports.SyncRecord{Provider: "p", Scope: "football", Attempted: t0, Success: true, Duration: time.Second})
	_ = r.RecordSync(ctx, ports.SyncRecord{Provider: "p", Scope: "football", Attempted: t0.Add(time.Minute), Err: "boom"})
	st, _ := r.GetSyncState(ctx, "p", "football")
	if st.ConsecutiveFailures != 1 || st.LastError != "boom" || st.LastSuccess.IsZero() {
		t.Errorf("state = %+v", st)
	}
}
