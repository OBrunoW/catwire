package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	defaultUploadTTL = 24 * time.Hour
	uploadSniffBytes = 512
	uploadStagedName = "upload"
	octetStream      = "application/octet-stream"
	maxFileNameBytes = 255
	oggApplication   = "application/ogg"
	oggAudio         = "audio/ogg"

	kindImage    = "image"
	kindAudio    = "audio"
	kindVideo    = "video"
	kindDocument = "document"
)

type DetailResource struct {
	domain.MediaResource
	InstanceID uuid.UUID  `json:"instance_id"`
	MessageID  *uuid.UUID `json:"message_id"`
}

type UploadInput struct {
	Body     io.Reader
	FileName string
	MimeType string
}

type OwnedInstanceFinder interface {
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Instance, error)
}

type UploadStore interface {
	Insert(ctx context.Context, media *domain.Media) error
	FindByID(ctx context.Context, clientID string, id uuid.UUID) (*domain.Media, error)
}

type LibraryOptions struct {
	Instances OwnedInstanceFinder
	Media     UploadStore
	Storage   Storage
	URLs      *URLBuilder
	MaxBytes  int64
	UploadTTL time.Duration
	TempDir   string
	Logger    *slog.Logger
	Now       func() time.Time
}

type Library struct {
	instances OwnedInstanceFinder
	media     UploadStore
	storage   Storage
	urls      *URLBuilder
	maxBytes  int64
	uploadTTL time.Duration
	tempDir   string
	logger    *slog.Logger
	now       func() time.Time
}

var ErrInvalidMaxBytes = errors.New("media: library max bytes must be positive")

var blockedMimeTypes = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
}

func NewLibrary(options LibraryOptions) (*Library, error) {
	if options.MaxBytes <= 0 {
		return nil, ErrInvalidMaxBytes
	}
	library := &Library{
		instances: options.Instances,
		media:     options.Media,
		storage:   options.Storage,
		urls:      options.URLs,
		maxBytes:  options.MaxBytes,
		uploadTTL: options.UploadTTL,
		tempDir:   options.TempDir,
		logger:    options.Logger,
		now:       options.Now,
	}
	if library.uploadTTL <= 0 {
		library.uploadTTL = defaultUploadTTL
	}
	if library.tempDir == "" {
		library.tempDir = os.TempDir()
	}
	if library.logger == nil {
		library.logger = slog.Default()
	}
	if library.now == nil {
		library.now = time.Now
	}
	return library, nil
}

func (l *Library) Get(ctx context.Context, clientID string, id uuid.UUID) (DetailResource, error) {
	found, err := l.media.FindByID(ctx, clientID, id)
	if err != nil {
		return DetailResource{}, err
	}
	return l.detail(*found), nil
}

func (l *Library) detail(item domain.Media) DetailResource {
	return DetailResource{
		MediaResource: l.urls.Resource(item),
		InstanceID:    item.InstanceID,
		MessageID:     item.MessageID,
	}
}

func (l *Library) Upload(ctx context.Context, clientID string, instanceID uuid.UUID, input UploadInput) (DetailResource, error) {
	owner, err := l.instances.FindByID(ctx, clientID, instanceID)
	if err != nil {
		return DetailResource{}, err
	}
	if err := validateFileName(input.FileName); err != nil {
		return DetailResource{}, err
	}
	declared, err := declaredMimeType(input.MimeType)
	if err != nil {
		return DetailResource{}, err
	}
	directory, err := os.MkdirTemp(l.tempDir, "catwire-upload-")
	if err != nil {
		return DetailResource{}, fmt.Errorf("media: create upload directory: %w", err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, uploadStagedName)
	size, err := l.stage(ctx, input.Body, path)
	if err != nil {
		return DetailResource{}, err
	}
	mimeType, err := resolveUploadMimeType(declared, path)
	if err != nil {
		return DetailResource{}, err
	}
	item, err := l.persist(ctx, *owner, input, path, mimeType, size)
	if err != nil {
		return DetailResource{}, err
	}
	return l.detail(*item), nil
}

func (l *Library) stage(ctx context.Context, body io.Reader, path string) (int64, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("media: create upload staging file: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(body, l.maxBytes+1))
	closeErr := file.Close()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if copyErr != nil {
		return 0, domain.BadRequest("could not read the uploaded file")
	}
	if closeErr != nil {
		return 0, fmt.Errorf("media: close upload staging file: %w", closeErr)
	}
	if written > l.maxBytes {
		return 0, domain.MediaTooLarge()
	}
	if written == 0 {
		return 0, domain.BadRequest("uploaded file is empty")
	}
	return written, nil
}

func (l *Library) persist(
	ctx context.Context,
	owner domain.Instance,
	input UploadInput,
	path, mimeType string,
	size int64,
) (*domain.Media, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("media: open staged upload: %w", err)
	}
	defer file.Close()
	item := l.newMedia(owner, input, mimeType, size)
	digest := sha256.New()
	if err := l.storage.Put(ctx, *item.StorageKey, io.TeeReader(file, digest), size, mimeType); err != nil {
		return nil, fmt.Errorf("media: store upload: %w", err)
	}
	checksum := hex.EncodeToString(digest.Sum(nil))
	item.SHA256 = &checksum
	if err := l.media.Insert(ctx, item); err != nil {
		l.discardObject(item)
		return nil, err
	}
	return item, nil
}

func (l *Library) newMedia(owner domain.Instance, input UploadInput, mimeType string, size int64) *domain.Media {
	now := l.now()
	id := uuid.New()
	backend := StorageBackendLocal
	key := fmt.Sprintf("%s/%04d/%02d/%s", owner.ID, now.UTC().Year(), int(now.UTC().Month()), id)
	expiresAt := now.Add(l.uploadTTL)
	kind := kindFromMimeType(mimeType)
	item := &domain.Media{
		ID:             id,
		InstanceID:     owner.ID,
		Kind:           kind,
		Status:         domain.MediaStatusStored,
		MimeType:       &mimeType,
		SizeBytes:      &size,
		StorageBackend: &backend,
		StorageKey:     &key,
		StoredAt:       &now,
		ExpiresAt:      &expiresAt,
	}
	if fileName := filepath.Base(strings.TrimSpace(input.FileName)); kind == kindDocument && fileName != "." && fileName != string(filepath.Separator) {
		item.FileName = &fileName
	}
	return item
}

func (l *Library) discardObject(item *domain.Media) {
	if err := l.storage.Delete(context.Background(), *item.StorageKey); err != nil {
		l.logger.Warn("discard uploaded media failed", "media_id", item.ID, "error_type", fmt.Sprintf("%T", err))
	}
}

func declaredMimeType(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	parsed, _, err := mime.ParseMediaType(raw)
	if err != nil || !strings.Contains(parsed, "/") {
		return "", domain.UnsupportedMediaType("mime_type is not a valid media type")
	}
	return parsed, nil
}

func validateFileName(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	base := filepath.Base(trimmed)
	if len(trimmed) > maxFileNameBytes || base == "." || base == ".." || base == string(filepath.Separator) {
		return domain.BadRequest("file_name is not a valid file name")
	}
	return nil
}

func mimeFamily(mimeType string) string {
	family, _, _ := strings.Cut(mimeType, "/")
	return family
}

func isMediaFamily(family string) bool {
	return family == kindImage || family == kindAudio || family == kindVideo
}

func familiesMatch(declared, detected string) bool {
	if declared == detected {
		return true
	}
	return (declared == kindAudio || declared == kindVideo) && (detected == kindAudio || detected == kindVideo)
}

func resolveUploadMimeType(declared, path string) (string, error) {
	detected, err := detectMimeType(path)
	if err != nil {
		return "", err
	}
	if blockedMimeTypes[detected] {
		return "", domain.UnsupportedMediaType("the file content type is not allowed")
	}
	if declared == "" || declared == octetStream {
		return detected, nil
	}
	return reconcileMimeTypes(declared, detected)
}

func reconcileMimeTypes(declared, detected string) (string, error) {
	if blockedMimeTypes[declared] {
		return "", domain.UnsupportedMediaType("mime_type is not allowed")
	}
	declaredFamily := mimeFamily(declared)
	if detected == octetStream {
		if !isMediaFamily(declaredFamily) {
			return "", domain.UnsupportedMediaType("could not confirm the declared media type from the file content")
		}
		return declared, nil
	}
	if !familiesMatch(declaredFamily, mimeFamily(detected)) {
		return "", domain.UnsupportedMediaType("mime_type does not match the file content")
	}
	return declared, nil
}

func detectMimeType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("media: open staged upload: %w", err)
	}
	defer file.Close()
	head := make([]byte, uploadSniffBytes)
	count, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("media: read staged upload: %w", err)
	}
	detected, _, err := mime.ParseMediaType(http.DetectContentType(head[:count]))
	if err != nil {
		return "", domain.UnsupportedMediaType("could not detect the media type of the file")
	}
	if detected == oggApplication {
		return oggAudio, nil
	}
	return detected, nil
}

func kindFromMimeType(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return kindImage
	case strings.HasPrefix(mimeType, "audio/"):
		return kindAudio
	case strings.HasPrefix(mimeType, "video/"):
		return kindVideo
	}
	return kindDocument
}
