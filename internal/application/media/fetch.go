package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	sourceFileName    = "source"
	convertedFileName = "converted.ogg"
	oggOpusMimeType   = "audio/ogg; codecs=opus"
	oggMimePrefix     = "audio/ogg"
	audioKind         = "audio"
	sniffLength       = 512
)

func (w *Worker) fetchAndStore(ctx context.Context, item domain.Media) (domain.StoredMedia, error) {
	directory, err := os.MkdirTemp(w.TempDir, "catwire-media-")
	if err != nil {
		return domain.StoredMedia{}, fmt.Errorf("media: create temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)

	sourcePath := filepath.Join(directory, sourceFileName)
	if err := w.download(ctx, item, sourcePath); err != nil {
		return domain.StoredMedia{}, err
	}
	finalPath := sourcePath
	mimeType := mimeTypeOf(item)
	if w.needsConversion(item, mimeType) {
		finalPath = filepath.Join(directory, convertedFileName)
		if err := w.Converter.ToOggOpus(ctx, sourcePath, finalPath); err != nil {
			return domain.StoredMedia{}, err
		}
		mimeType = oggOpusMimeType
	}
	return w.store(ctx, item, finalPath, mimeType)
}

func (w *Worker) download(ctx context.Context, item domain.Media, path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("media: create download file: %w", err)
	}
	request := DownloadRequest{
		InstanceID:    item.InstanceID,
		Kind:          item.Kind,
		DirectPath:    *item.WADirectPath,
		MediaKey:      item.WAMediaKey,
		FileSHA256:    item.WAFileSHA256,
		FileEncSHA256: item.WAFileEncSHA256,
	}
	downloadErr := w.Downloader.Download(ctx, request, newLimitedFile(file, w.MaxBytes))
	closeErr := file.Close()
	if downloadErr != nil {
		return SanitizeDownloadError(downloadErr)
	}
	if closeErr != nil {
		return fmt.Errorf("media: close download file: %w", closeErr)
	}
	return w.checkSize(path)
}

func (w *Worker) checkSize(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("media: stat download file: %w", err)
	}
	if w.MaxBytes > 0 && info.Size() > w.MaxBytes {
		return Permanent(CodeMediaTooLarge, fmt.Errorf("downloaded size %d exceeds limit %d", info.Size(), w.MaxBytes))
	}
	return nil
}

func mimeTypeOf(item domain.Media) string {
	if item.MimeType == nil {
		return ""
	}
	return *item.MimeType
}

func (w *Worker) needsConversion(item domain.Media, mimeType string) bool {
	if w.Converter == nil || item.Kind != audioKind || !item.IsVoiceNote {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(mimeType), oggMimePrefix)
}

func (w *Worker) store(ctx context.Context, item domain.Media, path, mimeType string) (domain.StoredMedia, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.StoredMedia{}, fmt.Errorf("media: open staged file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return domain.StoredMedia{}, fmt.Errorf("media: stat staged file: %w", err)
	}
	if mimeType == "" {
		mimeType, err = sniffMimeType(file)
		if err != nil {
			return domain.StoredMedia{}, err
		}
	}
	if err := w.checkSize(path); err != nil {
		return domain.StoredMedia{}, err
	}
	key := w.storageKey(item)
	digest := sha256.New()
	if err := w.Storage.Put(ctx, key, io.TeeReader(file, digest), info.Size(), mimeType); err != nil {
		return domain.StoredMedia{}, err
	}
	return domain.StoredMedia{
		StorageBackend: StorageBackendLocal,
		StorageKey:     key,
		MimeType:       mimeType,
		SizeBytes:      info.Size(),
		SHA256:         hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func sniffMimeType(file *os.File) (string, error) {
	head := make([]byte, sniffLength)
	count, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("media: read staged file: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("media: rewind staged file: %w", err)
	}
	return http.DetectContentType(head[:count]), nil
}

func (w *Worker) storageKey(item domain.Media) string {
	createdAt := item.CreatedAt.UTC()
	return fmt.Sprintf("%s/%04d/%02d/%s", item.InstanceID, createdAt.Year(), int(createdAt.Month()), item.ID)
}
