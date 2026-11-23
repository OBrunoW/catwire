package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/OBrunoW/catwire/internal/application/media"
)

type MediaRetryHandler interface {
	HandleRetry(
		ctx context.Context,
		instanceID uuid.UUID,
		waMessageID string,
		decrypt func(mediaKey []byte) (media.RetryResult, error),
	) error
}

type ClientRegistry struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]*whatsmeow.Client
}

func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{clients: make(map[uuid.UUID]*whatsmeow.Client)}
}

func (r *ClientRegistry) set(instanceID uuid.UUID, raw *whatsmeow.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[instanceID] = raw
}

func (r *ClientRegistry) remove(instanceID uuid.UUID, raw *whatsmeow.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients[instanceID] == raw {
		delete(r.clients, instanceID)
	}
}

func (r *ClientRegistry) get(instanceID uuid.UUID) (*whatsmeow.Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	raw, found := r.clients[instanceID]
	return raw, found
}

type MediaGateway struct {
	registry *ClientRegistry
}

func NewMediaGateway(registry *ClientRegistry) *MediaGateway {
	return &MediaGateway{registry: registry}
}

func (g *MediaGateway) Download(ctx context.Context, request media.DownloadRequest, destination media.DownloadTarget) error {
	raw, err := g.connected(request.InstanceID)
	if err != nil {
		return err
	}
	mediaType, known := mediaTypeForKind(request.Kind)
	if !known {
		return media.Permanent(media.CodeMediaUnavailable, fmt.Errorf("unsupported media kind %q", request.Kind))
	}
	err = raw.DownloadMediaWithPathToFile(
		ctx, request.DirectPath, request.FileEncSHA256, request.FileSHA256, request.MediaKey, mediaType, "", false, destination,
	)
	return classifyDownloadError(err)
}

func (g *MediaGateway) RequestRetry(ctx context.Context, instanceID uuid.UUID, target media.RetryTarget) error {
	raw, err := g.connected(instanceID)
	if err != nil {
		return err
	}
	chat, err := types.ParseJID(target.ChatJID)
	if err != nil {
		return fmt.Errorf("whatsapp: parse chat jid: %w", err)
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: target.FromMe, IsGroup: target.IsGroup()},
		ID:            types.MessageID(target.WAMessageID),
	}
	if target.IsGroup() {
		sender, err := types.ParseJID(target.SenderJID)
		if err != nil {
			return fmt.Errorf("whatsapp: parse sender jid: %w", err)
		}
		info.Sender = sender
	}
	return raw.SendMediaRetryReceipt(ctx, info, target.MediaKey)
}

func (g *MediaGateway) connected(instanceID uuid.UUID) (*whatsmeow.Client, error) {
	raw, found := g.registry.get(instanceID)
	if !found || !raw.IsConnected() {
		return nil, media.ErrInstanceDisconnected
	}
	return raw, nil
}

func mediaTypeForKind(kind string) (whatsmeow.MediaType, bool) {
	switch kind {
	case "image", "sticker":
		return whatsmeow.MediaImage, true
	case "video":
		return whatsmeow.MediaVideo, true
	case "audio":
		return whatsmeow.MediaAudio, true
	case "document":
		return whatsmeow.MediaDocument, true
	}
	return "", false
}

func classifyDownloadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404), errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410):
		return fmt.Errorf("%w: status %d", media.ErrMediaGone, mediaGoneStatus(err))
	case errors.Is(err, whatsmeow.ErrNoURLPresent),
		errors.Is(err, whatsmeow.ErrInvalidMediaSHA256),
		errors.Is(err, whatsmeow.ErrInvalidMediaEncSHA256),
		errors.Is(err, whatsmeow.ErrInvalidMediaHMAC):
		return media.Permanent(media.CodeMediaUnavailable, err)
	}
	return media.SanitizeDownloadError(err)
}

func mediaGoneStatus(err error) int {
	if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) {
		return http.StatusGone
	}
	return http.StatusNotFound
}

func (h *messageHandler) handleMediaRetry(event *events.MediaRetry) error {
	if h.retries == nil {
		return nil
	}
	decrypt := func(mediaKey []byte) (media.RetryResult, error) {
		return decryptRetry(event, mediaKey)
	}
	return h.retries.HandleRetry(h.ctx, h.instance.ID, event.MessageID, decrypt)
}

func decryptRetry(event *events.MediaRetry, mediaKey []byte) (media.RetryResult, error) {
	notification, err := whatsmeow.DecryptMediaRetryNotification(event, mediaKey)
	if err != nil {
		return media.RetryResult{}, err
	}
	if notification.GetResult() != waMmsRetry.MediaRetryNotification_SUCCESS {
		return media.RetryResult{Failure: "retry result " + notification.GetResult().String()}, nil
	}
	return media.RetryResult{DirectPath: notification.GetDirectPath()}, nil
}
