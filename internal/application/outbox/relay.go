package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	defaultBatchSize      = 100
	defaultPollInterval   = time.Second
	defaultPublishTimeout = 10 * time.Second
)

type RelayOptions struct {
	Unit           UnitOfWork
	Publisher      Publisher
	BatchSize      int
	PollInterval   time.Duration
	PublishTimeout time.Duration
	Logger         *slog.Logger
	Now            func() time.Time
}

type Relay struct {
	RelayOptions
}

func NewRelay(options RelayOptions) *Relay {
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.PublishTimeout <= 0 {
		options.PublishTimeout = defaultPublishTimeout
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Relay{RelayOptions: options}
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

func (r *Relay) drain(ctx context.Context) {
	for ctx.Err() == nil {
		fetched, err := r.RunOnce(ctx)
		if err != nil {
			r.Logger.Error("outbox relay failed", "error", err)
			return
		}
		if fetched == 0 {
			return
		}
	}
}

func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	var fetched int
	err := r.Unit.Do(ctx, func(unit Unit) error {
		events, err := unit.Outbox.FetchPending(ctx, r.BatchSize)
		if err != nil {
			return err
		}
		fetched = len(events)
		return r.process(ctx, unit, events)
	})
	if err != nil {
		return 0, err
	}
	return fetched, nil
}

type batchResult struct {
	published  []int64
	dispatched []int64
}

func (r *Relay) process(ctx context.Context, unit Unit, events []domain.OutboxEvent) error {
	var result batchResult
	instances := instanceCache{}
	for _, event := range events {
		if ctx.Err() != nil {
			break
		}
		if err := r.processEvent(ctx, unit, instances, event, &result); err != nil {
			return err
		}
	}
	return r.commitMarks(ctx, unit, result)
}

func (r *Relay) processEvent(
	ctx context.Context,
	unit Unit,
	instances instanceCache,
	event domain.OutboxEvent,
	result *batchResult,
) error {
	body, err := r.prepareBody(ctx, unit, instances, event)
	if err != nil {
		return err
	}
	if body == nil {
		return nil
	}
	publishedNow, err := r.publishToKafka(ctx, unit, event, body)
	if err != nil {
		return err
	}
	if publishedNow == publishFailed {
		return nil
	}
	if publishedNow == publishSent {
		result.published = append(result.published, event.Seq)
	}
	if err := r.fanOut(ctx, unit, event, body); err != nil {
		return err
	}
	result.dispatched = append(result.dispatched, event.Seq)
	return nil
}

func (r *Relay) prepareBody(ctx context.Context, unit Unit, instances instanceCache, event domain.OutboxEvent) ([]byte, error) {
	var instance *domain.EnvelopeInstance
	if event.InstanceID != nil {
		resolved, err := instances.resolve(ctx, unit.Instances, *event.InstanceID)
		if err != nil {
			return nil, err
		}
		instance = resolved
	}
	body, err := buildEnvelope(event, instance)
	if err != nil {
		return nil, r.recordFailure(ctx, unit, event, err)
	}
	return body, nil
}

type publishOutcome int

const (
	publishSent publishOutcome = iota
	publishSkipped
	publishFailed
)

func (r *Relay) publishToKafka(ctx context.Context, unit Unit, event domain.OutboxEvent, body []byte) (publishOutcome, error) {
	if r.Publisher == nil || event.KafkaPublishedAt != nil {
		return publishSkipped, nil
	}
	publishCtx, cancel := context.WithTimeout(ctx, r.PublishTimeout)
	defer cancel()
	if err := r.Publisher.Publish(publishCtx, kafkaMessage(event, body)); err != nil {
		r.Logger.Warn("outbox kafka publish failed", "seq", event.Seq, "event_type", event.EventType, "error", err)
		if recordErr := r.recordFailure(ctx, unit, event, fmt.Errorf("kafka publish: %w", err)); recordErr != nil {
			return publishFailed, recordErr
		}
		return publishFailed, nil
	}
	return publishSent, nil
}

func (r *Relay) recordFailure(ctx context.Context, unit Unit, event domain.OutboxEvent, cause error) error {
	next := r.Now().Add(domain.RetryBackoff(event.Attempts + 1))
	return unit.Outbox.RecordFailure(ctx, event.Seq, cause.Error(), next)
}

func (r *Relay) fanOut(ctx context.Context, unit Unit, event domain.OutboxEvent, body []byte) error {
	instanceID := uuid.Nil
	if event.InstanceID != nil {
		instanceID = *event.InstanceID
	}
	webhooks, err := unit.Webhooks.ListMatching(ctx, event.ClientID, instanceID, event.EventType)
	if err != nil {
		return err
	}
	for _, webhook := range webhooks {
		delivery := &domain.WebhookDelivery{
			ID:        uuid.New(),
			WebhookID: webhook.ID,
			EventID:   event.EventID,
			EventType: event.EventType,
			Payload:   body,
		}
		if _, err := unit.Deliveries.Insert(ctx, delivery); err != nil {
			return err
		}
	}
	return nil
}

func (r *Relay) commitMarks(ctx context.Context, unit Unit, result batchResult) error {
	if len(result.published) > 0 {
		if err := unit.Outbox.MarkKafkaPublished(ctx, result.published); err != nil {
			return err
		}
	}
	if len(result.dispatched) > 0 {
		if err := unit.Outbox.MarkDispatched(ctx, result.dispatched); err != nil {
			return err
		}
	}
	return nil
}
