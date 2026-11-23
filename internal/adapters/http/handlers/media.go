package handlers

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	octetStreamType     = "application/octet-stream"
	attachmentType      = "attachment"
	uploadFileField     = "file"
	uploadMimeTypeField = "mime_type"
	uploadMemoryBytes   = 8 << 20
	uploadOverheadBytes = 1 << 20
	documentKind        = "document"
)

type MediaLibrary interface {
	Upload(ctx context.Context, clientID string, instanceID uuid.UUID, input media.UploadInput) (media.DetailResource, error)
	Get(ctx context.Context, clientID string, id uuid.UUID) (media.DetailResource, error)
}

type MediaContent interface {
	OpenSigned(ctx context.Context, mediaID uuid.UUID, expires, signature string) (*media.Content, error)
	OpenForClient(ctx context.Context, clientID string, mediaID uuid.UUID) (*media.Content, error)
}

type MediaHandler struct {
	library      MediaLibrary
	content      MediaContent
	signedAccess bool
	maxBytes     int64
}

func NewMediaHandler(library MediaLibrary, content MediaContent, signedAccess bool, maxBytes int64) *MediaHandler {
	return &MediaHandler{library: library, content: content, signedAccess: signedAccess, maxBytes: maxBytes}
}

func (h *MediaHandler) Upload(w http.ResponseWriter, r *http.Request) {
	instanceID, err := pathUUID(r, "instance_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes+uploadOverheadBytes)
	if err := r.ParseMultipartForm(uploadMemoryBytes); err != nil {
		apihttp.WriteError(w, uploadFormError(err))
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile(uploadFileField)
	if err != nil {
		apihttp.WriteError(w, domain.BadRequest("file is required"))
		return
	}
	defer file.Close()
	uploaded, err := h.library.Upload(r.Context(), clientID(r), instanceID, media.UploadInput{
		Body:     file,
		FileName: header.Filename,
		MimeType: r.FormValue(uploadMimeTypeField),
	})
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.Created(w, uploaded)
}

func uploadFormError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return domain.MediaTooLarge()
	}
	return domain.BadRequest("body must be multipart/form-data with a file field")
}

func (h *MediaHandler) Get(w http.ResponseWriter, r *http.Request) {
	mediaID, err := pathUUID(r, "media_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	found, err := h.library.Get(r.Context(), clientID(r), mediaID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	apihttp.OK(w, found)
}

func (h *MediaHandler) Content(auth func(http.Handler) http.Handler) http.Handler {
	authenticated := auth(http.HandlerFunc(h.contentForClient))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Has("signature") || query.Has("expires") {
			h.contentSigned(w, r)
			return
		}
		authenticated.ServeHTTP(w, r)
	})
}

func (h *MediaHandler) contentSigned(w http.ResponseWriter, r *http.Request) {
	if !h.signedAccess {
		apihttp.WriteError(w, domain.Unauthorized("signed media urls are not enabled"))
		return
	}
	mediaID, err := pathUUID(r, "media_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	query := r.URL.Query()
	opened, err := h.content.OpenSigned(r.Context(), mediaID, query.Get("expires"), query.Get("signature"))
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	serveContent(w, r, opened)
}

func (h *MediaHandler) contentForClient(w http.ResponseWriter, r *http.Request) {
	mediaID, err := pathUUID(r, "media_id")
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	opened, err := h.content.OpenForClient(r.Context(), clientID(r), mediaID)
	if err != nil {
		apihttp.WriteError(w, err)
		return
	}
	serveContent(w, r, opened)
}

func serveContent(w http.ResponseWriter, r *http.Request, opened *media.Content) {
	defer opened.Body.Close()
	item := opened.Media
	w.Header().Set("Content-Type", contentType(item))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if disposition := contentDisposition(item); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	http.ServeContent(w, r, "", storedAt(item), opened.Body)
}

func contentType(item domain.Media) string {
	if !isInlineSafe(item) {
		return octetStreamType
	}
	return *item.MimeType
}

func isInlineSafe(item domain.Media) bool {
	if item.MimeType == nil || *item.MimeType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(*item.MimeType)
	if err != nil {
		return false
	}
	if mediaType == "application/pdf" {
		return true
	}
	for _, prefix := range []string{"image/", "audio/", "video/"} {
		if strings.HasPrefix(mediaType, prefix) && mediaType != "image/svg+xml" {
			return true
		}
	}
	return false
}

func contentDisposition(item domain.Media) string {
	hasFileName := item.FileName != nil && *item.FileName != ""
	if item.Kind != documentKind && isInlineSafe(item) {
		return ""
	}
	if !hasFileName {
		return attachmentType
	}
	return mime.FormatMediaType(attachmentType, map[string]string{"filename": *item.FileName})
}

func storedAt(item domain.Media) time.Time {
	if item.StoredAt == nil {
		return time.Time{}
	}
	return *item.StoredAt
}
