package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

func insertMediaWithCleanup(t *testing.T, pool *pgxpool.Pool, instanceID uuid.UUID, status string) *domain.Media {
	t.Helper()
	media := insertMedia(t, pool, instanceID, status, nil)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.media WHERE id = $1`, media.ID)
	})
	return media
}

func newStoredMedia() domain.StoredMedia {
	return domain.StoredMedia{
		StorageBackend: "local",
		StorageKey:     "media/" + uuid.NewString(),
		MimeType:       "image/jpeg",
		SizeBytes:      2048,
		SHA256:         "abc123",
	}
}

func TestMediaRescheduleChangesOnlyNextAttemptOfPendingMedia(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMediaRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	pending := insertMediaWithCleanup(t, pool, instance.ID, domain.MediaStatusPending)
	ctx := context.Background()
	requireNoError(t, repository.RecordDownloadFailure(ctx, pending.ID, "timeout", ptrTime(microsecondsNow().Add(time.Minute))))
	newAttemptAt := microsecondsNow().Add(2 * time.Hour)

	requireNoError(t, repository.Reschedule(ctx, pending.ID, newAttemptAt))

	updated, err := repository.FindByIDUnscoped(ctx, pending.ID)
	requireNoError(t, err)
	if updated.NextAttemptAt == nil || !updated.NextAttemptAt.Equal(newAttemptAt) {
		t.Fatalf("expected next_attempt_at %v, got %v", newAttemptAt, updated.NextAttemptAt)
	}
	if updated.DownloadAttempts != 1 || updated.Status != domain.MediaStatusPending {
		t.Fatalf("unexpected media %+v", updated)
	}
}

func TestMediaRescheduleIgnoresNonPendingMedia(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{name: "stored", status: domain.MediaStatusStored},
		{name: "failed", status: domain.MediaStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTestPool(t)
			repository := postgres.NewMediaRepository(pool)
			instance := newInstance(t, pool, uniqueClientID())
			media := insertMediaWithCleanup(t, pool, instance.ID, test.status)
			ctx := context.Background()
			before, err := repository.FindByIDUnscoped(ctx, media.ID)
			requireNoError(t, err)

			err = repository.Reschedule(ctx, media.ID, microsecondsNow().Add(2*time.Hour))

			requireAppError(t, err, domain.MediaNotFound())
			after, findErr := repository.FindByIDUnscoped(ctx, media.ID)
			requireNoError(t, findErr)
			if after.Status != before.Status || after.DownloadAttempts != before.DownloadAttempts {
				t.Fatalf("media changed: before %+v after %+v", before, after)
			}
			if (before.NextAttemptAt == nil) != (after.NextAttemptAt == nil) ||
				(before.NextAttemptAt != nil && !before.NextAttemptAt.Equal(*after.NextAttemptAt)) {
				t.Fatalf("next_attempt_at changed from %v to %v", before.NextAttemptAt, after.NextAttemptAt)
			}
		})
	}
}

func TestMediaRescheduleUnknownMediaFails(t *testing.T) {
	pool := newTestPool(t)

	err := postgres.NewMediaRepository(pool).Reschedule(context.Background(), uuid.New(), microsecondsNow())

	requireAppError(t, err, domain.MediaNotFound())
}

func TestMediaMarkStoredTransitionsPendingMedia(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMediaRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	pending := insertMediaWithCleanup(t, pool, instance.ID, domain.MediaStatusPending)
	ctx := context.Background()
	payload := newStoredMedia()

	requireNoError(t, repository.MarkStored(ctx, pending.ID, payload))

	updated, err := repository.FindByIDUnscoped(ctx, pending.ID)
	requireNoError(t, err)
	if updated.Status != domain.MediaStatusStored || updated.StoredAt == nil || updated.NextAttemptAt != nil || updated.LastError != nil {
		t.Fatalf("unexpected media %+v", updated)
	}
	if updated.StorageKey == nil || *updated.StorageKey != payload.StorageKey || updated.SizeBytes == nil || *updated.SizeBytes != payload.SizeBytes {
		t.Fatalf("stored fields not persisted: %+v", updated)
	}
}

func TestMediaMarkStoredTwiceFailsAndKeepsRow(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMediaRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	pending := insertMediaWithCleanup(t, pool, instance.ID, domain.MediaStatusPending)
	ctx := context.Background()
	first := newStoredMedia()
	requireNoError(t, repository.MarkStored(ctx, pending.ID, first))
	before, err := repository.FindByIDUnscoped(ctx, pending.ID)
	requireNoError(t, err)

	err = repository.MarkStored(ctx, pending.ID, newStoredMedia())

	requireAppError(t, err, domain.MediaNotFound())
	after, findErr := repository.FindByIDUnscoped(ctx, pending.ID)
	requireNoError(t, findErr)
	if after.StorageKey == nil || *after.StorageKey != first.StorageKey {
		t.Fatalf("storage_key changed: %v", after.StorageKey)
	}
	if after.StoredAt == nil || !after.StoredAt.Equal(*before.StoredAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("row changed: before %+v after %+v", before, after)
	}
}
