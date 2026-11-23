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

func insertMedia(t *testing.T, pool *pgxpool.Pool, instanceID uuid.UUID, status string, messageID *uuid.UUID) *domain.Media {
	t.Helper()
	expiresAt := microsecondsNow().Add(time.Hour)
	media := &domain.Media{
		ID:         uuid.New(),
		InstanceID: instanceID,
		MessageID:  messageID,
		Kind:       "image",
		Status:     status,
		ExpiresAt:  &expiresAt,
	}
	requireNoError(t, postgres.NewMediaRepository(pool).Insert(context.Background(), media))
	return media
}

func TestMediaAttachRequiresStoredAndUnattached(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		alreadyAttach  bool
		expectAttached bool
	}{
		{name: "stored and unattached", status: domain.MediaStatusStored, expectAttached: true},
		{name: "pending", status: domain.MediaStatusPending},
		{name: "failed", status: domain.MediaStatusFailed},
		{name: "skipped", status: domain.MediaStatusSkipped},
		{name: "stored but already attached", status: domain.MediaStatusStored, alreadyAttach: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTestPool(t)
			repository := postgres.NewMediaRepository(pool)
			instance := newInstance(t, pool, uniqueClientID())
			chat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
			targetMessage := insertMessage(t, pool, newMessage(instance.ID, chat.ID, domain.MessageStatusSent, microsecondsNow()))
			var existingMessageID *uuid.UUID
			if test.alreadyAttach {
				existing := insertMessage(t, pool, newMessage(instance.ID, chat.ID, domain.MessageStatusSent, microsecondsNow()))
				existingMessageID = &existing.ID
			}
			media := insertMedia(t, pool, instance.ID, test.status, existingMessageID)

			err := repository.Attach(context.Background(), instance.ID, media.ID, targetMessage.ID)

			stored, findErr := repository.FindByIDUnscoped(context.Background(), media.ID)
			requireNoError(t, findErr)
			if !test.expectAttached {
				requireAppError(t, err, domain.MediaNotFound())
				if stored.MessageID != existingMessageID && (stored.MessageID == nil || existingMessageID == nil || *stored.MessageID != *existingMessageID) {
					t.Fatalf("message_id changed to %v", stored.MessageID)
				}
				return
			}
			requireNoError(t, err)
			if stored.MessageID == nil || *stored.MessageID != targetMessage.ID {
				t.Fatalf("expected message_id %s, got %v", targetMessage.ID, stored.MessageID)
			}
			if stored.ExpiresAt != nil {
				t.Fatalf("expected expires_at cleared, got %v", stored.ExpiresAt)
			}
		})
	}
}

func TestMediaAttachWithOtherInstanceFails(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMediaRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	otherInstance := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
	message := insertMessage(t, pool, newMessage(instance.ID, chat.ID, domain.MessageStatusSent, microsecondsNow()))
	media := insertMedia(t, pool, instance.ID, domain.MediaStatusStored, nil)

	err := repository.Attach(context.Background(), otherInstance.ID, media.ID, message.ID)

	requireAppError(t, err, domain.MediaNotFound())
}

func TestMediaFindByIDScopedToClient(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMediaRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	media := insertMedia(t, pool, instance.ID, domain.MediaStatusStored, nil)

	found, err := repository.FindByID(context.Background(), instance.ClientID, media.ID)
	requireNoError(t, err)
	if found.ID != media.ID {
		t.Fatalf("expected %s, got %s", media.ID, found.ID)
	}

	_, err = repository.FindByID(context.Background(), uniqueClientID(), media.ID)
	requireAppError(t, err, domain.MediaNotFound())
}

func TestMediaFindByIDHidesMediaOfDeletedInstance(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMediaRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	media := insertMedia(t, pool, instance.ID, domain.MediaStatusStored, nil)
	requireNoError(t, postgres.NewInstanceRepository(pool).SoftDelete(context.Background(), instance.ClientID, instance.ID))

	_, err := repository.FindByID(context.Background(), instance.ClientID, media.ID)

	requireAppError(t, err, domain.MediaNotFound())
}

func TestMediaRecordDownloadFailure(t *testing.T) {
	retryAt := microsecondsNow().Add(time.Hour)
	tests := []struct {
		name           string
		nextAttemptAt  *time.Time
		expectedStatus string
	}{
		{name: "with retry stays pending", nextAttemptAt: &retryAt, expectedStatus: domain.MediaStatusPending},
		{name: "without retry becomes failed", nextAttemptAt: nil, expectedStatus: domain.MediaStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTestPool(t)
			repository := postgres.NewMediaRepository(pool)
			instance := newInstance(t, pool, uniqueClientID())
			media := insertMedia(t, pool, instance.ID, domain.MediaStatusPending, nil)

			err := repository.RecordDownloadFailure(context.Background(), media.ID, "timeout", test.nextAttemptAt)

			requireNoError(t, err)
			stored, err := repository.FindByIDUnscoped(context.Background(), media.ID)
			requireNoError(t, err)
			if stored.Status != test.expectedStatus || stored.DownloadAttempts != 1 {
				t.Fatalf("unexpected media %+v", stored)
			}
		})
	}
}
