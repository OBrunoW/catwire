package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	defaultBatchSize    = 20
	defaultPollInterval = 2 * time.Second
	leaseMargin         = time.Minute
)

type DispatcherOptions struct {
	Store        DispatchStore
	Sender       Sender
	MaxAttempts  int
	Timeout      time.Duration
	BatchSize    int
	PollInterval time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
}

type Dispatcher struct {
	DispatcherOptions
	lease time.Duration
}

func NewDispatcher(options DispatcherOptions) *Dispatcher {
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 10
	}
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Second
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Dispatcher{DispatcherOptions: options, lease: options.Timeout + leaseMargin}
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.drain(ctx)
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) {
	for ctx.Err() == nil {
		claimed, err := d.RunOnce(ctx)
		if err != nil {
			d.Logger.Error("webhook dispatcher claim failed", "error", err)
			return
		}
		if claimed < d.BatchSize {
			return
		}
	}
}

func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	due, err := d.Store.ClaimDue(ctx, d.BatchSize, d.lease)
	if err != nil {
		return 0, err
	}
	var group sync.WaitGroup
	for _, delivery := range due {
		group.Add(1)
		go func() {
			defer group.Done()
			d.deliver(ctx, delivery)
		}()
	}
	group.Wait()
	return len(due), nil
}

func (d *Dispatcher) deliver(ctx context.Context, due domain.DueDelivery) {
	sendCtx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	statusCode, err := d.Sender.Send(sendCtx, Request{
		URL:        due.WebhookURL,
		Secret:     due.WebhookSecret,
		DeliveryID: due.ID,
		EventID:    due.EventID,
		EventType:  due.EventType,
		Body:       due.Payload,
		Timestamp:  d.Now(),
	})
	if err == nil && statusCode >= 200 && statusCode < 300 {
		d.recordSuccess(ctx, due, statusCode)
		return
	}
	if ctx.Err() != nil {
		return
	}
	d.recordFailure(ctx, due, statusCode, err)
}

func (d *Dispatcher) recordSuccess(ctx context.Context, due domain.DueDelivery, statusCode int) {
	if err := d.Store.RecordSuccess(ctx, due.ID, statusCode); err != nil {
		d.Logger.Error("webhook success bookkeeping failed", "delivery_id", due.ID, "error", err)
	}
}

func (d *Dispatcher) recordFailure(ctx context.Context, due domain.DueDelivery, statusCode int, cause error) {
	var recordedStatus *int
	if statusCode != 0 {
		recordedStatus = &statusCode
	}
	message := failureMessage(statusCode, cause)
	next := d.Now().Add(domain.RetryBackoff(due.Attempts + 1))
	status, err := d.Store.RecordFailure(ctx, due.ID, recordedStatus, message, next, d.MaxAttempts)
	if err != nil {
		d.Logger.Error("webhook failure bookkeeping failed", "delivery_id", due.ID, "error", err)
		return
	}
	d.Logger.Warn("webhook delivery failed",
		"delivery_id", due.ID, "webhook_id", due.WebhookID, "attempt", due.Attempts+1, "status", status, "status_code", statusCode)
}

func failureMessage(statusCode int, cause error) string {
	if cause != nil {
		return cause.Error()
	}
	return fmt.Sprintf("unexpected response status %d", statusCode)
}
