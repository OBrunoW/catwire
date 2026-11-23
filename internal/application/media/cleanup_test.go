package media

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func expiredMedia(storageKey *string, expiresAt time.Time, attached bool) *domain.Media {
	item := &domain.Media{ID: uuid.New(), InstanceID: uuid.New(), Status: domain.MediaStatusStored, StorageKey: storageKey, ExpiresAt: &expiresAt}
	if attached {
		messageID := uuid.New()
		item.MessageID = &messageID
	}
	return item
}

func TestCleanerRemovesExpiredUnattachedMedia(t *testing.T) {
	db := newFakeDB()
	storage := newFakeStorage()
	key := "i/2026/11/expired"
	storage.objects[key] = []byte("x")
	expired := expiredMedia(&key, fixedNow.Add(-time.Minute), false)
	fresh := expiredMedia(nil, fixedNow.Add(time.Hour), false)
	attached := expiredMedia(nil, fixedNow.Add(-time.Minute), true)
	db.media = []*domain.Media{expired, fresh, attached}

	removed, err := NewCleaner(db, storage).RunOnce(context.Background(), 10)

	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || len(db.media) != 2 || db.find(expired.ID) != nil {
		t.Fatalf("removed = %d remaining = %d", removed, len(db.media))
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != key {
		t.Fatalf("deleted objects = %v", storage.deleted)
	}
}

func TestCleanerRemovesRowWithoutStorageKey(t *testing.T) {
	db := newFakeDB()
	storage := newFakeStorage()
	db.media = []*domain.Media{expiredMedia(nil, fixedNow.Add(-time.Minute), false)}

	removed, err := NewCleaner(db, storage).RunOnce(context.Background(), 10)

	if err != nil || removed != 1 || len(storage.deleted) != 0 {
		t.Fatalf("removed = %d err = %v", removed, err)
	}
}

type failingDeleteStorage struct {
	*fakeStorage
}

func (s failingDeleteStorage) Delete(context.Context, string) error { return errBoom }

func TestCleanerKeepsRowWhenStorageDeleteFails(t *testing.T) {
	db := newFakeDB()
	key := "k"
	item := expiredMedia(&key, fixedNow.Add(-time.Minute), false)
	db.media = []*domain.Media{item}

	removed, err := NewCleaner(db, failingDeleteStorage{newFakeStorage()}).RunOnce(context.Background(), 10)

	if !errors.Is(err, errBoom) || removed != 0 || db.find(item.ID) == nil {
		t.Fatalf("removed = %d err = %v", removed, err)
	}
}
