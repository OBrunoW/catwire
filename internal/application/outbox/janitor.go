package outbox

import (
	"context"
	"time"
)

const (
	DefaultRetention = 7 * 24 * time.Hour
	JanitorInterval  = time.Hour
)

type Janitor struct {
	store     RetentionStore
	retention time.Duration
	now       func() time.Time
}

func NewJanitor(store RetentionStore, retention time.Duration, now func() time.Time) *Janitor {
	if retention <= 0 {
		retention = DefaultRetention
	}
	if now == nil {
		now = time.Now
	}
	return &Janitor{store: store, retention: retention, now: now}
}

func (j *Janitor) RunOnce(ctx context.Context) (int64, error) {
	return j.store.DeleteDispatchedBefore(ctx, j.now().Add(-j.retention))
}
