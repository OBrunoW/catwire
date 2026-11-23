package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	instanceColumns = "id, client_id, external_id, name, status, status_reason, auto_connect, jid, phone, lid, " +
		"push_name, business_name, platform, connected_at, disconnected_at, deleted_at, created_at, updated_at"

	instanceExternalUniqueIndex = "instances_client_external_uq"
)

type InstanceRepository struct {
	db DBTX
}

func NewInstanceRepository(db DBTX) *InstanceRepository {
	return &InstanceRepository{db: db}
}

func scanInstance(row scanner) (domain.Instance, error) {
	var instance domain.Instance
	err := row.Scan(
		&instance.ID, &instance.ClientID, &instance.ExternalID, &instance.Name, &instance.Status,
		&instance.StatusReason, &instance.AutoConnect, &instance.JID, &instance.Phone, &instance.LID,
		&instance.PushName, &instance.BusinessName, &instance.Platform, &instance.ConnectedAt,
		&instance.DisconnectedAt, &instance.DeletedAt, &instance.CreatedAt, &instance.UpdatedAt,
	)
	return instance, err
}

func (r *InstanceRepository) Insert(ctx context.Context, instance *domain.Instance) error {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.instances (id, client_id, external_id, name, status, auto_connect)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		instance.ID, instance.ClientID, instance.ExternalID, instance.Name, instance.Status, instance.AutoConnect,
	).Scan(&instance.CreatedAt, &instance.UpdatedAt)
	if constraint, unique := uniqueConstraint(err); unique && constraint == instanceExternalUniqueIndex {
		return domain.InstanceAlreadyExists()
	}
	if err != nil {
		return wrapError("insert instance", err)
	}
	return nil
}

func (r *InstanceRepository) FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+instanceColumns+` FROM catwire.instances
		WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL`,
		id, clientID,
	)
	return instanceOrNotFound(row, "find instance")
}

func (r *InstanceRepository) FindUnscoped(ctx context.Context, id uuid.UUID) (*domain.Instance, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+instanceColumns+` FROM catwire.instances WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	return instanceOrNotFound(row, "find instance")
}

func instanceOrNotFound(row scanner, operation string) (*domain.Instance, error) {
	instance, err := scanInstance(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.InstanceNotFound()
	}
	if err != nil {
		return nil, wrapError(operation, err)
	}
	return &instance, nil
}

func (r *InstanceRepository) ListByClient(
	ctx context.Context,
	clientID string,
	filter domain.InstanceFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.Instance], error) {
	builder := &queryBuilder{}
	builder.where("client_id = " + builder.arg(clientID))
	builder.where("deleted_at IS NULL")
	if filter.Status != "" {
		builder.where("status = " + builder.arg(filter.Status))
	}
	if filter.ExternalID != "" {
		builder.where("external_id = " + builder.arg(filter.ExternalID))
	}
	if filter.Search != "" {
		pattern := builder.arg(likePattern(filter.Search))
		builder.where("(name ILIKE " + pattern + " OR phone ILIKE " + pattern + " OR external_id ILIKE " + pattern + ")")
	}
	var position timePosition
	hasCursor, err := decodeCursor(params, &position)
	if err != nil {
		return apihttp.PageResult[domain.Instance]{}, err
	}
	if hasCursor {
		builder.where("(created_at, id) < (" + builder.arg(position.At) + ", " + builder.arg(position.ID) + ")")
	}
	return queryPage(ctx, r.db, builder,
		`SELECT `+instanceColumns+` FROM catwire.instances`,
		"created_at DESC, id DESC", params, scanInstance,
		func(last domain.Instance) any { return timePosition{At: last.CreatedAt, ID: last.ID} },
	)
}

func (r *InstanceRepository) ListReconnectable(ctx context.Context) ([]domain.Instance, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+instanceColumns+` FROM catwire.instances
		WHERE jid IS NOT NULL AND auto_connect AND deleted_at IS NULL
		ORDER BY created_at, id`,
	)
	if err != nil {
		return nil, wrapError("list reconnectable instances", err)
	}
	instances, err := collect(rows, scanInstance)
	if err != nil {
		return nil, wrapError("list reconnectable instances", err)
	}
	return instances, nil
}

func (r *InstanceRepository) Update(
	ctx context.Context,
	clientID string,
	id uuid.UUID,
	update domain.InstanceUpdate,
) (*domain.Instance, error) {
	row := r.db.QueryRow(ctx,
		`UPDATE catwire.instances
		SET name = COALESCE($3, name), external_id = COALESCE($4, external_id)
		WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL
		RETURNING `+instanceColumns,
		id, clientID, update.Name, update.ExternalID,
	)
	instance, err := scanInstance(row)
	if constraint, unique := uniqueConstraint(err); unique && constraint == instanceExternalUniqueIndex {
		return nil, domain.InstanceAlreadyExists()
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.InstanceNotFound()
	}
	if err != nil {
		return nil, wrapError("update instance", err)
	}
	return &instance, nil
}

func (r *InstanceRepository) UpdateStatus(
	ctx context.Context,
	id uuid.UUID,
	change domain.InstanceStatusChange,
) (string, error) {
	var previousStatus string
	err := r.db.QueryRow(ctx,
		`WITH previous AS (
			SELECT id, status FROM catwire.instances WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
		)
		UPDATE catwire.instances AS target
		SET status = $2,
			status_reason = $3,
			connected_at = COALESCE($4, target.connected_at),
			disconnected_at = COALESCE($5, target.disconnected_at)
		FROM previous
		WHERE target.id = previous.id
		RETURNING previous.status`,
		id, change.Status, change.Reason, change.ConnectedAt, change.DisconnectedAt,
	).Scan(&previousStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.InstanceNotFound()
	}
	if err != nil {
		return "", wrapError("update instance status", err)
	}
	return previousStatus, nil
}

func (r *InstanceRepository) UpdateIdentity(ctx context.Context, id uuid.UUID, identity domain.InstanceIdentity) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.instances
		SET jid = $2, phone = $3, lid = $4, push_name = $5, business_name = $6, platform = $7
		WHERE id = $1 AND deleted_at IS NULL`,
		id, identity.JID, identity.Phone, identity.LID, identity.PushName, identity.BusinessName, identity.Platform,
	)
	if err != nil {
		return wrapError("update instance identity", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.InstanceNotFound()
	}
	return nil
}

func (r *InstanceRepository) SetAutoConnect(ctx context.Context, id uuid.UUID, autoConnect bool) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.instances SET auto_connect = $2 WHERE id = $1 AND deleted_at IS NULL`,
		id, autoConnect,
	)
	if err != nil {
		return wrapError("set instance auto connect", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.InstanceNotFound()
	}
	return nil
}

func (r *InstanceRepository) SoftDelete(ctx context.Context, clientID string, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.instances SET deleted_at = now(), auto_connect = false
		WHERE id = $1 AND client_id = $2 AND deleted_at IS NULL`,
		id, clientID,
	)
	if err != nil {
		return wrapError("delete instance", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.InstanceNotFound()
	}
	return nil
}
