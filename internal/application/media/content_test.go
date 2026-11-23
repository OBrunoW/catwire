package media

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type finderFake struct {
	db *fakeDB
}

func (f finderFake) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Media, error) {
	for _, item := range f.db.media {
		if item.ID != id {
			continue
		}
		for _, instance := range f.db.instances {
			if instance.ID == item.InstanceID && instance.ClientID == clientID {
				return item, nil
			}
		}
	}
	return nil, domain.MediaNotFound()
}

func (f finderFake) FindByIDUnscoped(_ context.Context, id uuid.UUID) (*domain.Media, error) {
	for _, item := range f.db.media {
		if item.ID == id {
			return item, nil
		}
	}
	return nil, domain.MediaNotFound()
}

func newContentFixture(t *testing.T) (*ContentService, *fakeDB, *fakeStorage, *Signer) {
	t.Helper()
	db := newFakeDB()
	storage := newFakeStorage()
	signer := newTestSigner(t, "secret")
	return NewContentService(finderFake{db: db}, storage, signer), db, storage, signer
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	appError, ok := domain.AsAppError(err)
	if !ok || appError.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestContentSignedOpensStoredMedia(t *testing.T) {
	service, db, storage, signer := newContentFixture(t)
	item := db.seed()
	key := "k"
	item.Status = domain.MediaStatusStored
	item.StorageKey = &key
	storage.objects[key] = []byte("payload")
	expires := expiresIn(time.Minute)

	content, err := service.OpenSigned(context.Background(), item.ID, expires, signer.Sign(item.ID, expires))

	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	body, _ := io.ReadAll(content.Body)
	if string(body) != "payload" || content.Media.ID != item.ID {
		t.Fatalf("body = %q", body)
	}
}

func TestContentSignedRejectsBadSignatureBeforeLookup(t *testing.T) {
	service, _, _, _ := newContentFixture(t)

	_, err := service.OpenSigned(context.Background(), uuid.New(), expiresIn(time.Minute), "00")

	requireUnauthorized(t, err)
}

func TestContentReturnsNotReadyUnlessStored(t *testing.T) {
	for _, status := range []string{domain.MediaStatusPending, domain.MediaStatusFailed, domain.MediaStatusSkipped} {
		t.Run(status, func(t *testing.T) {
			service, db, _, signer := newContentFixture(t)
			item := db.seed()
			item.Status = status
			expires := expiresIn(time.Minute)

			_, signedErr := service.OpenSigned(context.Background(), item.ID, expires, signer.Sign(item.ID, expires))
			_, clientErr := service.OpenForClient(context.Background(), "client-a", item.ID)

			requireCode(t, signedErr, "MEDIA_NOT_READY")
			requireCode(t, clientErr, "MEDIA_NOT_READY")
		})
	}
}

func TestContentForClientIsScopedToOwner(t *testing.T) {
	service, db, storage, _ := newContentFixture(t)
	item := db.seed()
	key := "k"
	item.Status = domain.MediaStatusStored
	item.StorageKey = &key
	storage.objects[key] = []byte("payload")

	_, err := service.OpenForClient(context.Background(), "client-b", item.ID)
	content, ownerErr := service.OpenForClient(context.Background(), "client-a", item.ID)

	requireCode(t, err, "MEDIA_NOT_FOUND")
	if ownerErr != nil {
		t.Fatal(ownerErr)
	}
	content.Body.Close()
}

func TestContentMissingObjectIsNotFound(t *testing.T) {
	service, db, _, _ := newContentFixture(t)
	item := db.seed()
	key := "gone"
	item.Status = domain.MediaStatusStored
	item.StorageKey = &key

	_, err := service.OpenForClient(context.Background(), "client-a", item.ID)

	requireCode(t, err, "MEDIA_NOT_FOUND")
}
