package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const deliveryColumns = "id, webhook_id, event_id, event_type, payload, status, attempts, next_attempt_at, " +
	"last_status_code, last_error, last_attempt_at, delivered_at, created_at, updated_at"

const deliveryOwnedByClient = `EXISTS (
	SELECT 1 FROM catwire.webhooks AS owner
	WHERE owner.id = webhook_deliveries.webhook_id AND owner.client_id = $1 AND owner.deleted_at IS NULL
)`

type WebhookDeliveryRepository struct {
	db DBTX
}

func NewWebhookDeliveryRepository(db DBTX) *WebhookDeliveryRepository {
	return &WebhookDeliveryRepository{db: db}
}

func deliveryDestinations(delivery *domain.WebhookDelivery) []any {
	return []any{
		&delivery.ID, &delivery.WebhookID, &delivery.EventID, &delivery.EventType, &delivery.Payload,
		&delivery.Status, &delivery.Attempts, &delivery.NextAttemptAt, &delivery.LastStatusCode,
		&delivery.LastError, &delivery.LastAttemptAt, &delivery.DeliveredAt, &delivery.CreatedAt,
		&delivery.UpdatedAt,
	}
}

func scanDelivery(row scanner) (domain.WebhookDelivery, error) {
	var delivery domain.WebhookDelivery
	err := row.Scan(deliveryDestinations(&delivery)...)
	return delivery, err
}

func scanDueDelivery(row scanner) (domain.DueDelivery, error) {
	var due domain.DueDelivery
	destinations := append(deliveryDestinations(&due.WebhookDelivery), &due.WebhookURL, &due.WebhookSecret)
	err := row.Scan(destinations...)
	return due, err
}

func (r *WebhookDeliveryRepository) Insert(ctx context.Context, delivery *domain.WebhookDelivery) (bool, error) {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.webhook_deliveries (id, webhook_id, event_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (webhook_id, event_id) DO NOTHING
		RETURNING status, attempts, next_attempt_at, created_at, updated_at`,
		delivery.ID, delivery.WebhookID, delivery.EventID, delivery.EventType, orEmptyJSON(delivery.Payload, "{}"),
	).Scan(&delivery.Status, &delivery.Attempts, &delivery.NextAttemptAt, &delivery.CreatedAt, &delivery.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, wrapError("insert webhook delivery", err)
	}
	return true, nil
}

func (r *WebhookDeliveryRepository) FindByID(
	ctx context.Context,
	clientID string,
	webhookID, id uuid.UUID,
) (*domain.WebhookDelivery, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+deliveryColumns+` FROM catwire.webhook_deliveries
		WHERE id = $2 AND webhook_id = $3 AND `+deliveryOwnedByClient,
		clientID, id, webhookID,
	)
	delivery, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.DeliveryNotFound()
	}
	if err != nil {
		return nil, wrapError("find webhook delivery", err)
	}
	return &delivery, nil
}

func (r *WebhookDeliveryRepository) ListByWebhook(
	ctx context.Context,
	clientID string,
	webhookID uuid.UUID,
	filter domain.DeliveryFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.WebhookDelivery], error) {
	builder := &queryBuilder{}
	builder.where("webhook_id = " + builder.arg(webhookID))
	builder.where(`EXISTS (SELECT 1 FROM catwire.webhooks AS owner WHERE owner.id = webhook_deliveries.webhook_id AND owner.client_id = ` +
		builder.arg(clientID) + ` AND owner.deleted_at IS NULL)`)
	if filter.Status != "" {
		builder.where("status = " + builder.arg(filter.Status))
	}
	if filter.EventType != "" {
		builder.where("event_type = " + builder.arg(filter.EventType))
	}
	var position timePosition
	hasCursor, err := decodeCursor(params, &position)
	if err != nil {
		return apihttp.PageResult[domain.WebhookDelivery]{}, err
	}
	if hasCursor {
		builder.where("(created_at, id) < (" + builder.arg(position.At) + ", " + builder.arg(position.ID) + ")")
	}
	return queryPage(ctx, r.db, builder,
		`SELECT `+deliveryColumns+` FROM catwire.webhook_deliveries`,
		"created_at DESC, id DESC", params, scanDelivery,
		func(last domain.WebhookDelivery) any { return timePosition{At: last.CreatedAt, ID: last.ID} },
	)
}

func (r *WebhookDeliveryRepository) ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]domain.DueDelivery, error) {
	rows, err := r.db.Query(ctx,
		`WITH claimed AS (
			UPDATE catwire.webhook_deliveries SET next_attempt_at = now() + make_interval(secs => $2)
			WHERE id IN (
				SELECT delivery.id FROM catwire.webhook_deliveries AS delivery
				JOIN catwire.webhooks AS owner ON owner.id = delivery.webhook_id
				WHERE delivery.status IN ('pending', 'failed') AND delivery.next_attempt_at <= now()
					AND owner.active AND owner.deleted_at IS NULL
				ORDER BY delivery.next_attempt_at, delivery.id
				LIMIT $1
				FOR UPDATE OF delivery SKIP LOCKED
			)
			RETURNING `+deliveryColumns+`
		)
		SELECT `+qualify("claimed", deliveryColumns)+`, owner.url, owner.secret
		FROM claimed
		JOIN catwire.webhooks AS owner ON owner.id = claimed.webhook_id
		ORDER BY claimed.next_attempt_at, claimed.id`,
		limit, lease.Seconds(),
	)
	if err != nil {
		return nil, wrapError("claim due deliveries", err)
	}
	due, err := collect(rows, scanDueDelivery)
	if err != nil {
		return nil, wrapError("claim due deliveries", err)
	}
	return due, nil
}

func (r *WebhookDeliveryRepository) RecordSuccess(ctx context.Context, id uuid.UUID, statusCode int) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.webhook_deliveries
		SET status = 'succeeded', attempts = attempts + 1, last_status_code = $2, last_error = NULL,
			last_attempt_at = now(), delivered_at = now()
		WHERE id = $1 AND status IN ('pending', 'failed')`,
		id, statusCode,
	)
	if err != nil {
		return wrapError("record delivery success", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.DeliveryNotFound()
	}
	return nil
}

func (r *WebhookDeliveryRepository) RecordFailure(
	ctx context.Context,
	id uuid.UUID,
	statusCode *int,
	message string,
	nextAttemptAt time.Time,
	maxAttempts int,
) (string, error) {
	var status string
	err := r.db.QueryRow(ctx,
		`UPDATE catwire.webhook_deliveries
		SET attempts = attempts + 1,
			status = CASE WHEN attempts + 1 >= $5 THEN 'dead' ELSE 'failed' END,
			last_status_code = $2,
			last_error = $3,
			last_attempt_at = now(),
			next_attempt_at = $4
		WHERE id = $1 AND status IN ('pending', 'failed')
		RETURNING status`,
		id, statusCode, message, nextAttemptAt, maxAttempts,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.DeliveryNotFound()
	}
	if err != nil {
		return "", wrapError("record delivery failure", err)
	}
	return status, nil
}

func (r *WebhookDeliveryRepository) ResetForRetry(
	ctx context.Context,
	clientID string,
	webhookID, id uuid.UUID,
) (*domain.WebhookDelivery, error) {
	row := r.db.QueryRow(ctx,
		`UPDATE catwire.webhook_deliveries
		SET status = 'pending', attempts = 0, next_attempt_at = now(), delivered_at = NULL
		WHERE id = $2 AND webhook_id = $3 AND status IN ('failed', 'dead') AND `+deliveryOwnedByClient+`
		RETURNING `+deliveryColumns,
		clientID, id, webhookID,
	)
	delivery, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.DeliveryNotFound()
	}
	if err != nil {
		return nil, wrapError("reset delivery for retry", err)
	}
	return &delivery, nil
}
