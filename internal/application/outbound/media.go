package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	oggOpusMimeType = "audio/ogg; codecs=opus"
	octetStreamMime = "application/octet-stream"
	sniffLength     = 512
	sourceFileName  = "source"
	convertedName   = "converted.ogg"
)

type mediaSource struct {
	body      io.ReadCloser
	mimeType  string
	fileName  string
	readError *domain.AppError
}

func (s *Service) prepareMedia(
	ctx context.Context,
	clientID string,
	instance domain.Instance,
	request SendRequest,
) (*domain.Media, error) {
	input := *request.Media
	source, stored, err := s.openSource(ctx, clientID, instance, request.Type, input)
	if err != nil {
		return nil, err
	}
	if stored != nil {
		return stored, nil
	}
	defer source.body.Close()
	return s.storeSource(ctx, instance, request.Type, input, source)
}

func (s *Service) openSource(
	ctx context.Context,
	clientID string,
	instance domain.Instance,
	messageType string,
	input MediaInput,
) (mediaSource, *domain.Media, error) {
	switch {
	case input.MediaID != nil:
		return s.openStoredSource(ctx, clientID, instance, messageType, input)
	case input.URL != "":
		source, err := s.openURLSource(ctx, input.URL)
		return source, nil, err
	}
	return mediaSource{body: io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(input.Base64))), readError: domain.BadRequest("invalid base64 media content")}, nil, nil
}

func (s *Service) openURLSource(ctx context.Context, rawURL string) (mediaSource, error) {
	fetched, err := s.fetcher.Fetch(ctx, rawURL)
	if err != nil {
		if ctx.Err() != nil {
			return mediaSource{}, ctx.Err()
		}
		return mediaSource{}, domain.MediaFetchFailed("could not fetch media from url")
	}
	return mediaSource{body: fetched.Body, mimeType: fetched.ContentType, fileName: fetched.FileName}, nil
}

func (s *Service) openStoredSource(
	ctx context.Context,
	clientID string,
	instance domain.Instance,
	messageType string,
	input MediaInput,
) (mediaSource, *domain.Media, error) {
	found, err := s.findAttachableMedia(ctx, clientID, instance, messageType, *input.MediaID)
	if err != nil {
		return mediaSource{}, nil, err
	}
	if !input.AsVoiceNote {
		return mediaSource{}, found, nil
	}
	body, err := s.storage.Open(ctx, *found.StorageKey)
	if errors.Is(err, media.ErrObjectNotFound) {
		return mediaSource{}, nil, domain.MediaNotFound()
	}
	if err != nil {
		return mediaSource{}, nil, fmt.Errorf("outbound: open stored media: %w", err)
	}
	return mediaSource{body: body, mimeType: stringValue(found.MimeType)}, nil, nil
}

func (s *Service) findAttachableMedia(
	ctx context.Context,
	clientID string,
	instance domain.Instance,
	messageType string,
	id uuid.UUID,
) (*domain.Media, error) {
	var found *domain.Media
	err := s.unit.Do(ctx, func(unit Unit) error {
		item, err := unit.Media.FindByID(ctx, clientID, id)
		found = item
		return err
	})
	if err != nil {
		return nil, err
	}
	if found.InstanceID != instance.ID || found.MessageID != nil || found.Status != domain.MediaStatusStored || found.StorageKey == nil {
		return nil, domain.MediaNotFound()
	}
	if found.Kind != messageType {
		return nil, domain.BadRequest("media kind does not match type")
	}
	if found.SizeBytes != nil && s.maxMediaBytes > 0 && *found.SizeBytes > s.maxMediaBytes {
		return nil, domain.MediaTooLarge()
	}
	return found, nil
}

func (s *Service) storeSource(
	ctx context.Context,
	instance domain.Instance,
	messageType string,
	input MediaInput,
	source mediaSource,
) (*domain.Media, error) {
	directory, err := os.MkdirTemp(s.tempDir, "catwire-send-")
	if err != nil {
		return nil, fmt.Errorf("outbound: create temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)
	sourcePath := filepath.Join(directory, sourceFileName)
	if err := s.stage(ctx, source, sourcePath); err != nil {
		return nil, err
	}
	mimeType, err := resolveMimeType(input.MimeType, source.mimeType, sourcePath)
	if err != nil {
		return nil, err
	}
	if !mimeMatchesType(messageType, mimeType) {
		return nil, domain.UnsupportedMediaType("mime type does not match the message type")
	}
	finalPath := sourcePath
	if input.AsVoiceNote {
		finalPath = filepath.Join(directory, convertedName)
		if err := s.convertVoiceNote(ctx, sourcePath, finalPath); err != nil {
			return nil, err
		}
		mimeType = oggOpusMimeType
	}
	return s.persist(ctx, instance, messageType, input, source, finalPath, mimeType)
}

func (s *Service) stage(ctx context.Context, source mediaSource, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("outbound: create staging file: %w", err)
	}
	reader := io.Reader(source.body)
	if s.maxMediaBytes > 0 {
		reader = io.LimitReader(reader, s.maxMediaBytes+1)
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if copyErr != nil && source.readError != nil {
		return source.readError
	}
	if copyErr != nil {
		return domain.MediaFetchFailed("could not read media content")
	}
	if closeErr != nil {
		return fmt.Errorf("outbound: close staging file: %w", closeErr)
	}
	if s.maxMediaBytes > 0 && written > s.maxMediaBytes {
		return domain.MediaTooLarge()
	}
	if written == 0 {
		return domain.MediaFetchFailed("media content is empty")
	}
	return nil
}

func (s *Service) convertVoiceNote(ctx context.Context, inputPath, outputPath string) error {
	if s.converter == nil {
		return domain.MediaFetchFailed("audio conversion is unavailable")
	}
	if err := s.converter.ToOggOpus(ctx, inputPath, outputPath); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return domain.MediaFetchFailed("could not convert audio to a voice note")
	}
	return nil
}

func (s *Service) persist(
	ctx context.Context,
	instance domain.Instance,
	messageType string,
	input MediaInput,
	source mediaSource,
	path, mimeType string,
) (*domain.Media, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("outbound: open staged media: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("outbound: stat staged media: %w", err)
	}
	if s.maxMediaBytes > 0 && info.Size() > s.maxMediaBytes {
		return nil, domain.MediaTooLarge()
	}
	item := s.newMedia(instance, messageType, input, source, mimeType, info.Size())
	digest := sha256.New()
	if err := s.storage.Put(ctx, *item.StorageKey, io.TeeReader(file, digest), info.Size(), mimeType); err != nil {
		return nil, fmt.Errorf("outbound: store media: %w", err)
	}
	checksum := hex.EncodeToString(digest.Sum(nil))
	item.SHA256 = &checksum
	if err := s.unit.Do(ctx, func(unit Unit) error { return unit.Media.Insert(ctx, item) }); err != nil {
		s.discardObject(item)
		return nil, err
	}
	return item, nil
}

func (s *Service) newMedia(
	instance domain.Instance,
	messageType string,
	input MediaInput,
	source mediaSource,
	mimeType string,
	size int64,
) *domain.Media {
	now := s.now()
	id := uuid.New()
	backend := media.StorageBackendLocal
	key := fmt.Sprintf("%s/%04d/%02d/%s", instance.ID, now.UTC().Year(), int(now.UTC().Month()), id)
	expiresAt := now.Add(s.unattachedTTL)
	item := &domain.Media{
		ID:             id,
		InstanceID:     instance.ID,
		Kind:           messageType,
		Status:         domain.MediaStatusStored,
		MimeType:       &mimeType,
		SizeBytes:      &size,
		IsVoiceNote:    input.AsVoiceNote,
		StorageBackend: &backend,
		StorageKey:     &key,
		StoredAt:       &now,
		ExpiresAt:      &expiresAt,
	}
	if messageType == MessageTypeDocument {
		item.FileName = documentFileName(input.FileName, source.fileName)
	}
	return item
}

func (s *Service) discardObject(item *domain.Media) {
	if err := s.storage.Delete(context.Background(), *item.StorageKey); err != nil {
		s.logger.Warn("discard staged media failed", "media_id", item.ID, "error_type", fmt.Sprintf("%T", err))
	}
}

func documentFileName(requested, fetched string) *string {
	for _, candidate := range []string{requested, fetched} {
		if candidate != "" {
			return &candidate
		}
	}
	return nil
}

func resolveMimeType(declared, reported, path string) (string, error) {
	for _, candidate := range []string{declared, reported} {
		if normalized := normalizeMimeType(candidate); normalized != "" && normalized != octetStreamMime {
			return normalized, nil
		}
	}
	return sniffMimeType(path)
}

func normalizeMimeType(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return ""
	}
	return parsed
}

func sniffMimeType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("outbound: open staged media: %w", err)
	}
	defer file.Close()
	head := make([]byte, sniffLength)
	count, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("outbound: read staged media: %w", err)
	}
	return normalizeMimeType(http.DetectContentType(head[:count])), nil
}

func mimeMatchesType(messageType, mimeType string) bool {
	switch messageType {
	case MessageTypeImage:
		return strings.HasPrefix(mimeType, "image/")
	case MessageTypeAudio:
		return strings.HasPrefix(mimeType, "audio/")
	case MessageTypeVideo:
		return strings.HasPrefix(mimeType, "video/")
	}
	return true
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
