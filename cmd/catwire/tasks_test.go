package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskGroupStopCancelsAndWaits(t *testing.T) {
	group := newTaskGroup(context.Background())
	var finished atomic.Bool
	group.Go(func(ctx context.Context) {
		<-ctx.Done()
		finished.Store(true)
	})
	if err := group.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !finished.Load() {
		t.Error("task must have finished before Stop returns")
	}
}

func TestTaskGroupStopHonorsDeadline(t *testing.T) {
	group := newTaskGroup(context.Background())
	release := make(chan struct{})
	defer close(release)
	group.Go(func(context.Context) { <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := group.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestRunEveryRunsImmediatelyAndOnEachTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	done := make(chan struct{})
	go func() {
		runEvery(ctx, 5*time.Millisecond, func(context.Context) { runs.Add(1) })
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for runs.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("expected at least 3 runs, got %d", runs.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestStopGroupIgnoresNilGroup(t *testing.T) {
	if err := stopGroup(nil)(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
