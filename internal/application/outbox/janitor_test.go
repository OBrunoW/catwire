package outbox

import (
	"context"
	"testing"
	"time"
)

type fakeRetention struct {
	dispatchedAt []time.Time
	cutoff       time.Time
}

func (r *fakeRetention) DeleteDispatchedBefore(_ context.Context, cutoff time.Time) (int64, error) {
	r.cutoff = cutoff
	kept := []time.Time{}
	var removed int64
	for _, at := range r.dispatchedAt {
		if !at.IsZero() && at.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, at)
	}
	r.dispatchedAt = kept
	return removed, nil
}

func TestJanitorRemovesOnlyDispatchedEventsOlderThanRetention(t *testing.T) {
	store := &fakeRetention{dispatchedAt: []time.Time{
		fixedNow.Add(-8 * 24 * time.Hour),
		fixedNow.Add(-6 * 24 * time.Hour),
		{},
	}}
	janitor := NewJanitor(store, 0, func() time.Time { return fixedNow })

	removed, err := janitor.RunOnce(context.Background())

	if err != nil || removed != 1 || len(store.dispatchedAt) != 2 {
		t.Fatalf("removed=%d kept=%d err=%v", removed, len(store.dispatchedAt), err)
	}
	if !store.cutoff.Equal(fixedNow.Add(-DefaultRetention)) {
		t.Fatalf("unexpected cutoff: %s", store.cutoff)
	}
}

func TestJanitorHonorsConfiguredRetention(t *testing.T) {
	store := &fakeRetention{}
	janitor := NewJanitor(store, 24*time.Hour, func() time.Time { return fixedNow })

	if _, err := janitor.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !store.cutoff.Equal(fixedNow.Add(-24 * time.Hour)) {
		t.Fatalf("unexpected cutoff: %s", store.cutoff)
	}
}
