package media

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type libraryInstances struct{ owned map[uuid.UUID]string }

func (f libraryInstances) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	if owner, found := f.owned[id]; !found || owner != clientID {
		return nil, domain.InstanceNotFound()
	}
	return &domain.Instance{ID: id, ClientID: clientID}, nil
}

type libraryMedia struct {
	items     map[uuid.UUID]*domain.Media
	insertErr error
}

func (f *libraryMedia) Insert(_ context.Context, item *domain.Media) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	stored := *item
	f.items[item.ID] = &stored
	return nil
}

func (f *libraryMedia) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Media, error) {
	item, found := f.items[id]
	if !found || clientID != "client-a" {
		return nil, domain.MediaNotFound()
	}
	copied := *item
	return &copied, nil
}

type libraryFixture struct {
	library    *Library
	media      *libraryMedia
	storage    *fakeStorage
	instanceID uuid.UUID
}

func newLibraryFixture(maxBytes int64) libraryFixture {
	instanceID := uuid.New()
	store := &libraryMedia{items: map[uuid.UUID]*domain.Media{}}
	storage := newFakeStorage()
	signer, _ := NewSigner("secret", func() time.Time { return fixedNow })
	library, err := NewLibrary(LibraryOptions{
		Instances: libraryInstances{owned: map[uuid.UUID]string{instanceID: "client-a"}},
		Media:     store,
		Storage:   storage,
		URLs:      NewURLBuilder(signer, "https://catwire.example.com", time.Hour),
		MaxBytes:  maxBytes,
		Now:       func() time.Time { return fixedNow },
	})
	if err != nil {
		panic(err)
	}
	return libraryFixture{library: library, media: store, storage: storage, instanceID: instanceID}
}

func assertCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Status != status || appErr.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

func TestUploadStoresUnattachedMediaWithDayExpiry(t *testing.T) {
	fixture := newLibraryFixture(1024)

	uploaded, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{
		Body:     strings.NewReader("%PDF-1.4 content"),
		FileName: "report.pdf",
		MimeType: "application/pdf",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := fixture.media.items[uploaded.ID]
	if stored.MessageID != nil || stored.Status != domain.MediaStatusStored || stored.Kind != "document" {
		t.Fatalf("unexpected media %+v", stored)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(fixedNow.Add(24*time.Hour)) {
		t.Fatalf("expected 24h expiry, got %v", stored.ExpiresAt)
	}
	if stored.FileName == nil || *stored.FileName != "report.pdf" || stored.SHA256 == nil {
		t.Fatalf("unexpected metadata %+v", stored)
	}
	if len(fixture.storage.puts) != 1 || uploaded.DownloadURL == nil || uploaded.InstanceID != fixture.instanceID {
		t.Fatalf("unexpected upload result %+v", uploaded)
	}
}

func TestUploadDetectsKindFromContent(t *testing.T) {
	fixture := newLibraryFixture(1024)
	pngHeader := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

	uploaded, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{Body: bytes.NewReader(pngHeader)})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uploaded.Kind != "image" || uploaded.MimeType == nil || *uploaded.MimeType != "image/png" {
		t.Fatalf("unexpected resource %+v", uploaded)
	}
}

func TestUploadAboveLimitIsRejectedAndNothingIsStored(t *testing.T) {
	fixture := newLibraryFixture(10)

	_, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{
		Body:     strings.NewReader("12345678901"),
		MimeType: "image/png",
	})

	assertCode(t, err, 413, "MEDIA_TOO_LARGE")
	if len(fixture.storage.puts) != 0 || len(fixture.media.items) != 0 {
		t.Fatal("rejected upload must not store anything")
	}
}

func TestUploadAtLimitIsAccepted(t *testing.T) {
	fixture := newLibraryFixture(10)

	_, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{
		Body:     strings.NewReader("\x89PNG\r\n\x1a\n12"),
		MimeType: "image/png",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadRejectsInvalidMimeType(t *testing.T) {
	fixture := newLibraryFixture(1024)

	_, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{
		Body:     strings.NewReader("content"),
		MimeType: "not a mime",
	})

	assertCode(t, err, 415, "UNSUPPORTED_MEDIA_TYPE")
}

func TestUploadRejectsEmptyFile(t *testing.T) {
	fixture := newLibraryFixture(1024)

	_, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{Body: strings.NewReader("")})

	assertCode(t, err, 400, "BAD_REQUEST")
}

func TestUploadToInstanceOfAnotherClientIsNotFound(t *testing.T) {
	fixture := newLibraryFixture(1024)

	_, err := fixture.library.Upload(context.Background(), "client-b", fixture.instanceID, UploadInput{Body: strings.NewReader("content")})

	assertCode(t, err, 404, "INSTANCE_NOT_FOUND")
}

func TestUploadDiscardsObjectWhenInsertFails(t *testing.T) {
	fixture := newLibraryFixture(1024)
	fixture.media.insertErr = domain.Internal()

	_, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{
		Body:     strings.NewReader("content"),
		MimeType: "text/plain",
	})

	assertCode(t, err, 500, "INTERNAL_ERROR")
	if len(fixture.storage.deleted) != 1 {
		t.Fatalf("expected the stored object to be removed, got %v", fixture.storage.deleted)
	}
}

func TestGetReturnsInstanceAndMessageIDsAndHidesOtherClients(t *testing.T) {
	fixture := newLibraryFixture(1024)
	messageID := uuid.New()
	item := &domain.Media{ID: uuid.New(), InstanceID: fixture.instanceID, MessageID: &messageID, Kind: "image", Status: domain.MediaStatusPending}
	fixture.media.items[item.ID] = item

	found, err := fixture.library.Get(context.Background(), "client-a", item.ID)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.InstanceID != fixture.instanceID || found.MessageID == nil || *found.MessageID != messageID || found.DownloadURL != nil {
		t.Fatalf("unexpected resource %+v", found)
	}
	_, err = fixture.library.Get(context.Background(), "client-b", item.ID)
	assertCode(t, err, 404, "MEDIA_NOT_FOUND")
}

func TestURLBuilderOmitsURLWithoutBaseURLOrStoredStatus(t *testing.T) {
	signer, _ := NewSigner("secret", func() time.Time { return fixedNow })
	stored := domain.Media{ID: uuid.New(), Status: domain.MediaStatusStored}

	withoutBase := NewURLBuilder(signer, "", time.Hour).Resource(stored)
	pending := NewURLBuilder(signer, "https://catwire.example.com", time.Hour).Resource(domain.Media{ID: uuid.New(), Status: domain.MediaStatusPending})
	signed := NewURLBuilder(signer, "https://catwire.example.com", time.Hour).Resource(stored)

	if withoutBase.DownloadURL != nil || pending.DownloadURL != nil {
		t.Fatal("download url must be null")
	}
	if signed.DownloadURL == nil || signed.DownloadURLExpiresAt == nil || !signed.DownloadURLExpiresAt.Equal(fixedNow.Add(time.Hour)) {
		t.Fatalf("unexpected signed resource %+v", signed)
	}
}

func uploadWith(fixture libraryFixture, body, fileName, mimeType string) error {
	_, err := fixture.library.Upload(context.Background(), "client-a", fixture.instanceID, UploadInput{
		Body:     strings.NewReader(body),
		FileName: fileName,
		MimeType: mimeType,
	})
	return err
}

func TestUploadRejectsDeclaredFamilyThatDiffersFromContent(t *testing.T) {
	fixture := newLibraryFixture(1024)

	err := uploadWith(fixture, "just some text", "", "image/png")

	assertCode(t, err, 415, "UNSUPPORTED_MEDIA_TYPE")
	if len(fixture.storage.puts) != 0 {
		t.Fatal("rejected upload must not store anything")
	}
}

func TestUploadRejectsHTMLDeclaredOrDetected(t *testing.T) {
	fixture := newLibraryFixture(1024)

	declared := uploadWith(fixture, "plain text", "", "text/html")
	detected := uploadWith(fixture, "<!DOCTYPE html><html><body>x</body></html>", "", "text/plain")
	sniffed := uploadWith(fixture, "<html><body>x</body></html>", "", "")

	assertCode(t, declared, 415, "UNSUPPORTED_MEDIA_TYPE")
	assertCode(t, detected, 415, "UNSUPPORTED_MEDIA_TYPE")
	assertCode(t, sniffed, 415, "UNSUPPORTED_MEDIA_TYPE")
}

func TestUploadAcceptsRealPDFDeclaredAsPDF(t *testing.T) {
	fixture := newLibraryFixture(1024)

	err := uploadWith(fixture, "%PDF-1.7 body", "a.pdf", "application/pdf")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadWithUndetectableContentRequiresMediaFamily(t *testing.T) {
	fixture := newLibraryFixture(1024)
	binary := "\x00\x01\x02\x03\xff\xfe"

	executable := uploadWith(fixture, binary, "", "application/x-msdownload")
	document := uploadWith(fixture, binary, "", "application/pdf")
	audio := uploadWith(fixture, binary, "", "audio/mpeg")

	assertCode(t, executable, 415, "UNSUPPORTED_MEDIA_TYPE")
	assertCode(t, document, 415, "UNSUPPORTED_MEDIA_TYPE")
	if audio != nil {
		t.Fatalf("unexpected error: %v", audio)
	}
}

func TestUploadAcceptsAudioDeclaredForVideoContainer(t *testing.T) {
	fixture := newLibraryFixture(1024)

	err := uploadWith(fixture, "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom", "voice.m4a", "audio/mp4")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewLibraryRequiresPositiveMaxBytes(t *testing.T) {
	for _, maxBytes := range []int64{0, -1} {
		library, err := NewLibrary(LibraryOptions{MaxBytes: maxBytes})

		if !errors.Is(err, ErrInvalidMaxBytes) || library != nil {
			t.Fatalf("max bytes %d: expected ErrInvalidMaxBytes, got %v", maxBytes, err)
		}
	}
}

func TestUploadRejectsInvalidFileNames(t *testing.T) {
	fixture := newLibraryFixture(1024)

	for _, fileName := range []string{".", "..", "dir/..", "/", strings.Repeat("a", 256)} {
		err := uploadWith(fixture, "%PDF-1.4 content", fileName, "application/pdf")
		assertCode(t, err, 400, "BAD_REQUEST")
	}
	if err := uploadWith(fixture, "%PDF-1.4 content", strings.Repeat("a", 255), "application/pdf"); err != nil {
		t.Fatalf("255 byte name must pass: %v", err)
	}
}
