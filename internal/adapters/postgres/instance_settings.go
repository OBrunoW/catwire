package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/OBrunoW/catwire/internal/domain"
)

const settingsColumns = "instance_id, sync_history, ignore_groups, auto_mark_read, always_online, download_media, created_at, updated_at"

type InstanceSettingsRepository struct {
	db DBTX
}

func NewInstanceSettingsRepository(db DBTX) *InstanceSettingsRepository {
	return &InstanceSettingsRepository{db: db}
}

func scanSettings(row scanner) (domain.InstanceSettings, error) {
	var settings domain.InstanceSettings
	err := row.Scan(
		&settings.InstanceID, &settings.SyncHistory, &settings.IgnoreGroups, &settings.AutoMarkRead,
		&settings.AlwaysOnline, &settings.DownloadMedia, &settings.CreatedAt, &settings.UpdatedAt,
	)
	return settings, err
}

func (r *InstanceSettingsRepository) Insert(ctx context.Context, settings *domain.InstanceSettings) error {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.instance_settings
			(instance_id, sync_history, ignore_groups, auto_mark_read, always_online, download_media)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		settings.InstanceID, settings.SyncHistory, settings.IgnoreGroups, settings.AutoMarkRead,
		settings.AlwaysOnline, settings.DownloadMedia,
	).Scan(&settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return wrapError("insert instance settings", err)
	}
	return nil
}

func (r *InstanceSettingsRepository) FindByInstanceID(ctx context.Context, instanceID uuid.UUID) (*domain.InstanceSettings, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+settingsColumns+` FROM catwire.instance_settings WHERE instance_id = $1`,
		instanceID,
	)
	settings, err := scanSettings(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.InstanceNotFound()
	}
	if err != nil {
		return nil, wrapError("find instance settings", err)
	}
	return &settings, nil
}

func (r *InstanceSettingsRepository) Update(ctx context.Context, settings *domain.InstanceSettings) error {
	err := r.db.QueryRow(ctx,
		`UPDATE catwire.instance_settings
		SET sync_history = $2, ignore_groups = $3, auto_mark_read = $4, always_online = $5, download_media = $6
		WHERE instance_id = $1
		RETURNING updated_at`,
		settings.InstanceID, settings.SyncHistory, settings.IgnoreGroups, settings.AutoMarkRead,
		settings.AlwaysOnline, settings.DownloadMedia,
	).Scan(&settings.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InstanceNotFound()
	}
	if err != nil {
		return wrapError("update instance settings", err)
	}
	return nil
}

func (r *InstanceSettingsRepository) FindByInstanceIDs(ctx context.Context, instanceIDs []uuid.UUID) (map[uuid.UUID]domain.InstanceSettings, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+settingsColumns+` FROM catwire.instance_settings WHERE instance_id = ANY($1)`,
		orEmptyUUIDs(instanceIDs),
	)
	if err != nil {
		return nil, wrapError("find instance settings by ids", err)
	}
	items, err := collect(rows, scanSettings)
	if err != nil {
		return nil, wrapError("find instance settings by ids", err)
	}
	byInstance := make(map[uuid.UUID]domain.InstanceSettings, len(items))
	for _, item := range items {
		byInstance[item.InstanceID] = item
	}
	return byInstance, nil
}
