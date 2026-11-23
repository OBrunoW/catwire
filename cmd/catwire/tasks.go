package main

import (
	"context"
	"sync"
	"time"
)

type taskGroup struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newTaskGroup(parent context.Context) *taskGroup {
	ctx, cancel := context.WithCancel(parent)
	return &taskGroup{ctx: ctx, cancel: cancel}
}

func (g *taskGroup) Go(run func(ctx context.Context)) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		run(g.ctx)
	}()
}

func (g *taskGroup) Stop(ctx context.Context) error {
	g.cancel()
	finished := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runEvery(ctx context.Context, interval time.Duration, task func(ctx context.Context)) {
	task(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			task(ctx)
		}
	}
}
