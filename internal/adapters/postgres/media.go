package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/OBrunoW/catwire/internal/domain"
)

const mediaColumns = "id, instance_id, message_id, kind, status, mime_type, file_name, size_bytes, sha256, width, height, " +
	"duration_seconds, is_voice_note, storage_backend, storage_key, wa_direct_path, wa_media_key, wa_file_sha256, " +
	"wa_file_enc_sha256, download_attempts, next_attempt_at, last_error, stored_at, expires_at, created_at, updated_at"

const mediaOwnedByClient = `EXISTS (
	SELECT 1 FROM catwire.instances AS owner
	WHERE owner.id = media.instance_id AND owner.client_id = $2 AND owner.deleted_at IS NULL
)`

type MediaRepository struct {
	db DBTX
}

func NewMediaRepository(db DBTX) *MediaRepository {
	return &MediaRepository{db: db}
}

func scanMedia(row scanner) (domain.Media, error) {
	var media domain.Media
	err := row.Scan(
		&media.ID, &media.InstanceID, &media.MessageID, &media.Kind, &media.Status, &media.MimeType,
		&media.FileName, &media.SizeBytes, &media.SHA256, &media.Width, &media.Height, &media.DurationSeconds,
		&media.IsVoiceNote, &media.StorageBackend, &media.StorageKey, &media.WADirectPath, &media.WAMediaKey,
		&media.WAFileSHA256, &media.WAFileEncSHA256, &media.DownloadAttempts, &media.NextAttemptAt,
		&media.LastError, &media.StoredAt, &media.ExpiresAt, &media.CreatedAt, &media.UpdatedAt,
	)
	return media, err
}

func (r *MediaRepository) Insert(ctx context.Context, media *domain.Media) error {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.media (
			id, instance_id, message_id, kind, status, mime_type, file_name, size_bytes, sha256, width, height,
			duration_seconds, is_voice_note, storage_backend, storage_key, wa_direct_path, wa_media_key,
			wa_file_sha256, wa_file_enc_sha256, next_attempt_at, stored_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
		)
		RETURNING created_at, updated_at`,
		media.ID, media.InstanceID, media.MessageID, media.Kind, media.Status, media.MimeType, media.FileName,
		media.SizeBytes, media.SHA256, media.Width, media.Height, media.DurationSeconds, media.IsVoiceNote,
		media.StorageBackend, media.StorageKey, media.WADirectPath, media.WAMediaKey, media.WAFileSHA256,
		media.WAFileEncSHA256, media.NextAttemptAt, media.StoredAt, media.ExpiresAt,
	).Scan(&media.CreatedAt, &media.UpdatedAt)
	if err != nil {
		return wrapError("insert media", err)
	}
	return nil
}

func (r *MediaRepository) FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Media, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+mediaColumns+` FROM catwire.media WHERE id = $1 AND `+mediaOwnedByClient,
		id, clientID,
	)
	return mediaOrNotFound(row, "find media")
}

func (r *MediaRepository) FindByIDUnscoped(ctx context.Context, id uuid.UUID) (*domain.Media, error) {
	row := r.db.QueryRow(ctx, `SELECT `+mediaColumns+` FROM catwire.media WHERE id = $1`, id)
	return mediaOrNotFound(row, "find media")
}

func mediaOrNotFound(row scanner, operation string) (*domain.Media, error) {
	media, err := scanMedia(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.MediaNotFound()
	}
	if err != nil {
		return nil, wrapError(operation, err)
	}
	return &media, nil
}

func (r *MediaRepository) FindByMessageIDs(ctx context.Context, messageIDs []uuid.UUID) ([]domain.Media, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+mediaColumns+` FROM catwire.media WHERE message_id = ANY($1)`,
		orEmptyUUIDs(messageIDs),
	)
	if err != nil {
		return nil, wrapError("find media by messages", err)
	}
	items, err := collect(rows, scanMedia)
	if err != nil {
		return nil, wrapError("find media by messages", err)
	}
	return items, nil
}

func (r *MediaRepository) Attach(ctx context.Context, instanceID, id, messageID uuid.UUID) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.media SET message_id = $3, expires_at = NULL
		WHERE id = $2 AND instance_id = $1 AND message_id IS NULL AND status = 'stored'`,
		instanceID, id, messageID,
	)
	if err != nil {
		return wrapError("attach media", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MediaNotFound()
	}
	return nil
}

func (r *MediaRepository) ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]domain.Media, error) {
	rows, err := r.db.Query(ctx,
		`UPDATE catwire.media SET next_attempt_at = now() + make_interval(secs => $2)
		WHERE id IN (
			SELECT id FROM catwire.media
			WHERE status = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= now())
			ORDER BY next_attempt_at NULLS FIRST, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+mediaColumns,
		limit, lease.Seconds(),
	)
	if err != nil {
		return nil, wrapError("claim pending media", err)
	}
	items, err := collect(rows, scanMedia)
	if err != nil {
		return nil, wrapError("claim pending media", err)
	}
	return items, nil
}

func (r *MediaRepository) MarkStored(ctx context.Context, id uuid.UUID, stored domain.StoredMedia) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.media
		SET status = 'stored', storage_backend = $2, storage_key = $3, mime_type = $4, size_bytes = $5,
			sha256 = $6, width = COALESCE($7, width), height = COALESCE($8, height),
			duration_seconds = COALESCE($9, duration_seconds), stored_at = now(), next_attempt_at = NULL,
			last_error = NULL
		WHERE id = $1 AND status = 'pending'`,
		id, stored.StorageBackend, stored.StorageKey, stored.MimeType, stored.SizeBytes, stored.SHA256,
		stored.Width, stored.Height, stored.DurationSeconds,
	)
	if err != nil {
		return wrapError("mark media stored", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MediaNotFound()
	}
	return nil
}

func (r *MediaRepository) Reschedule(ctx context.Context, id uuid.UUID, nextAttemptAt time.Time) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.media SET next_attempt_at = $2 WHERE id = $1 AND status = 'pending'`,
		id, nextAttemptAt,
	)
	if err != nil {
		return wrapError("reschedule media", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MediaNotFound()
	}
	return nil
}

func (r *MediaRepository) RecordDownloadFailure(
	ctx context.Context,
	id uuid.UUID,
	message string,
	nextAttemptAt *time.Time,
) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.media
		SET download_attempts = download_attempts + 1,
			last_error = $2,
			next_attempt_at = $3,
			status = CASE WHEN $3::timestamptz IS NULL THEN 'failed' ELSE 'pending' END
		WHERE id = $1`,
		id, message, nextAttemptAt,
	)
	if err != nil {
		return wrapError("record media download failure", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MediaNotFound()
	}
	return nil
}

func (r *MediaRepository) ApplyRetryPath(ctx context.Context, id uuid.UUID, directPath string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.media
		SET wa_direct_path = $2, next_attempt_at = now(), last_error = NULL
		WHERE id = $1 AND status = 'pending'`,
		id, directPath,
	)
	if err != nil {
		return wrapError("apply media retry path", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MediaNotFound()
	}
	return nil
}

func (r *MediaRepository) ListExpiredUnattached(ctx context.Context, limit int) ([]domain.Media, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+mediaColumns+` FROM catwire.media
		WHERE message_id IS NULL AND expires_at IS NOT NULL AND expires_at <= now()
		ORDER BY expires_at
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, wrapError("list expired media", err)
	}
	items, err := collect(rows, scanMedia)
	if err != nil {
		return nil, wrapError("list expired media", err)
	}
	return items, nil
}

func (r *MediaRepository) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM catwire.media WHERE id = $1`, id); err != nil {
		return wrapError("delete media", err)
	}
	return nil
}
