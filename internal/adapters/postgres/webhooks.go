package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const webhookColumns = "id, client_id, url, description, secret, events, instance_ids, active, deleted_at, created_at, updated_at"

type WebhookRepository struct {
	db DBTX
}

func NewWebhookRepository(db DBTX) *WebhookRepository {
	return &WebhookRepository{db: db}
}

func scanWebhook(row scanner) (domain.Webhook, error) {
	var webhook domain.Webhook
	err := row.Scan(
		&webhook.ID, &webhook.ClientID, &webhook.URL, &webhook.Description, &webhook.Secret, &webhook.Events,
		&webhook.InstanceIDs, &webhook.Active, &webhook.DeletedAt, &webhook.CreatedAt, &webhook.UpdatedAt,
	)
	return webhook, err
}

func (r *WebhookRepository) Insert(ctx context.Context, webhook *domain.Webhook) error {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.webhooks (id, client_id, url, description, secret, events, instance_ids, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at`,
		webhook.ID, webhook.ClientID, webhook.URL, webhook.Description, webhook.Secret,
		orEmptyStrings(webhook.Events), orEmptyUUIDs(webhook.InstanceIDs), webhook.Active,
	).Scan(&webhook.CreatedAt, &webhook.UpdatedAt)
	if err != nil {
		return wrapError("insert webhook", err)
	}
	return nil
}

func (r *WebhookRepository) FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Webhook, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+webhookColumns+` FROM catwire.webhooks
		WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL`,
		id, clientID,
	)
	webhook, err := scanWebhook(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.WebhookNotFound()
	}
	if err != nil {
		return nil, wrapError("find webhook", err)
	}
	return &webhook, nil
}

func (r *WebhookRepository) ListByClient(
	ctx context.Context,
	clientID string,
	active *bool,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.Webhook], error) {
	builder := &queryBuilder{}
	builder.where("client_id = " + builder.arg(clientID))
	builder.where("deleted_at IS NULL")
	if active != nil {
		builder.where("active = " + builder.arg(*active))
	}
	var position timePosition
	hasCursor, err := decodeCursor(params, &position)
	if err != nil {
		return apihttp.PageResult[domain.Webhook]{}, err
	}
	if hasCursor {
		builder.where("(created_at, id) < (" + builder.arg(position.At) + ", " + builder.arg(position.ID) + ")")
	}
	return queryPage(ctx, r.db, builder,
		`SELECT `+webhookColumns+` FROM catwire.webhooks`,
		"created_at DESC, id DESC", params, scanWebhook,
		func(last domain.Webhook) any { return timePosition{At: last.CreatedAt, ID: last.ID} },
	)
}

func (r *WebhookRepository) ListMatching(
	ctx context.Context,
	clientID string,
	instanceID uuid.UUID,
	eventType string,
) ([]domain.Webhook, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+webhookColumns+` FROM catwire.webhooks
		WHERE client_id = $1 AND active AND deleted_at IS NULL
			AND (cardinality(events) = 0 OR $3::text = ANY(events))
			AND (cardinality(instance_ids) = 0 OR $2::uuid = ANY(instance_ids))
		ORDER BY created_at, id`,
		clientID, instanceID, eventType,
	)
	if err != nil {
		return nil, wrapError("list matching webhooks", err)
	}
	webhooks, err := collect(rows, scanWebhook)
	if err != nil {
		return nil, wrapError("list matching webhooks", err)
	}
	return webhooks, nil
}

func (r *WebhookRepository) Update(
	ctx context.Context,
	clientID string,
	id uuid.UUID,
	update domain.WebhookUpdate,
) (*domain.Webhook, error) {
	var events []string
	if update.Events != nil {
		events = orEmptyStrings(*update.Events)
	}
	var instanceIDs []uuid.UUID
	if update.InstanceIDs != nil {
		instanceIDs = orEmptyUUIDs(*update.InstanceIDs)
	}
	row := r.db.QueryRow(ctx,
		`UPDATE catwire.webhooks
		SET url = COALESCE($3, url),
			description = COALESCE($4, description),
			events = COALESCE($5, events),
			instance_ids = COALESCE($6, instance_ids),
			active = COALESCE($7, active)
		WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL
		RETURNING `+webhookColumns,
		id, clientID, update.URL, update.Description, events, instanceIDs, update.Active,
	)
	webhook, err := scanWebhook(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.WebhookNotFound()
	}
	if err != nil {
		return nil, wrapError("update webhook", err)
	}
	return &webhook, nil
}

func (r *WebhookRepository) RotateSecret(ctx context.Context, clientID string, id uuid.UUID, secret string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.webhooks SET secret = $3 WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL`,
		id, clientID, secret,
	)
	if err != nil {
		return wrapError("rotate webhook secret", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.WebhookNotFound()
	}
	return nil
}

func (r *WebhookRepository) SoftDelete(ctx context.Context, clientID string, id uuid.UUID) error {
	var deleted int
	err := r.db.QueryRow(ctx,
		`WITH removed AS (
			UPDATE catwire.webhooks SET deleted_at = now(), active = false
			WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL
			RETURNING id
		), abandoned AS (
			UPDATE catwire.webhook_deliveries SET status = 'dead'
			WHERE webhook_id IN (SELECT id FROM removed) AND status IN ('pending', 'failed')
			RETURNING id
		)
		SELECT count(*) FROM removed`,
		id, clientID,
	).Scan(&deleted)
	if err != nil {
		return wrapError("delete webhook", err)
	}
	if deleted == 0 {
		return domain.WebhookNotFound()
	}
	return nil
}
