package postgres

import (
	"context"
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

const outboxColumns = "seq, event_id, event_type, event_version, client_id, instance_id, partition_key, payload, " +
	"occurred_at, dispatched_at, kafka_published_at, attempts, next_attempt_at, last_error, created_at"

type OutboxRepository struct {
	db DBTX
}

func NewOutboxRepository(db DBTX) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func scanOutboxEvent(row scanner) (domain.OutboxEvent, error) {
	var event domain.OutboxEvent
	err := row.Scan(
		&event.Seq, &event.EventID, &event.EventType, &event.EventVersion, &event.ClientID, &event.InstanceID,
		&event.PartitionKey, &event.Payload, &event.OccurredAt, &event.DispatchedAt, &event.KafkaPublishedAt,
		&event.Attempts, &event.NextAttemptAt, &event.LastError, &event.CreatedAt,
	)
	return event, err
}

func (r *OutboxRepository) Insert(ctx context.Context, event *domain.OutboxEvent) error {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.outbox_events
			(event_id, event_type, event_version, client_id, instance_id, partition_key, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING seq, attempts, next_attempt_at, created_at`,
		event.EventID, event.EventType, event.EventVersion, event.ClientID, event.InstanceID,
		event.PartitionKey, orEmptyJSON(event.Payload, "{}"), event.OccurredAt,
	).Scan(&event.Seq, &event.Attempts, &event.NextAttemptAt, &event.CreatedAt)
	if err != nil {
		return wrapError("insert outbox event", err)
	}
	return nil
}

func (r *OutboxRepository) FetchPending(ctx context.Context, tx DBTX, limit int) ([]domain.OutboxEvent, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+qualify("candidate", outboxColumns)+` FROM catwire.outbox_events AS candidate
		WHERE candidate.dispatched_at IS NULL AND candidate.next_attempt_at <= now()
			AND NOT EXISTS (
				SELECT 1 FROM catwire.outbox_events AS earlier
				WHERE earlier.partition_key = candidate.partition_key
					AND earlier.dispatched_at IS NULL
					AND earlier.seq < candidate.seq
			)
		ORDER BY candidate.seq
		LIMIT $1
		FOR UPDATE OF candidate SKIP LOCKED`,
		limit,
	)
	if err != nil {
		return nil, wrapError("fetch pending outbox events", err)
	}
	events, err := collect(rows, scanOutboxEvent)
	if err != nil {
		return nil, wrapError("fetch pending outbox events", err)
	}
	return events, nil
}

func (r *OutboxRepository) MarkKafkaPublished(ctx context.Context, seqs []int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE catwire.outbox_events SET kafka_published_at = now() WHERE seq = ANY($1)`,
		seqs,
	)
	if err != nil {
		return wrapError("mark outbox kafka published", err)
	}
	return nil
}

func (r *OutboxRepository) MarkDispatched(ctx context.Context, seqs []int64) error {
	_, err := r.db.Exec(ctx,
		`UPDATE catwire.outbox_events SET dispatched_at = now(), last_error = NULL WHERE seq = ANY($1)`,
		seqs,
	)
	if err != nil {
		return wrapError("mark outbox dispatched", err)
	}
	return nil
}

func (r *OutboxRepository) RecordFailure(ctx context.Context, seq int64, message string, nextAttemptAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE catwire.outbox_events
		SET attempts = attempts + 1, last_error = $2, next_attempt_at = $3
		WHERE seq = $1`,
		seq, message, nextAttemptAt,
	)
	if err != nil {
		return wrapError("record outbox failure", err)
	}
	return nil
}

func (r *OutboxRepository) DeleteDispatchedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM catwire.outbox_events WHERE dispatched_at IS NOT NULL AND dispatched_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, wrapError("delete dispatched outbox events", err)
	}
	return tag.RowsAffected(), nil
}
