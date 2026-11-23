package media

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type Content struct {
	Media domain.Media
	Body  io.ReadSeekCloser
}

type ContentService struct {
	finder  MediaFinder
	storage Storage
	signer  *Signer
}

func NewContentService(finder MediaFinder, storage Storage, signer *Signer) *ContentService {
	return &ContentService{finder: finder, storage: storage, signer: signer}
}

func (s *ContentService) OpenSigned(ctx context.Context, mediaID uuid.UUID, expires, signature string) (*Content, error) {
	if err := s.signer.Verify(mediaID, expires, signature); err != nil {
		return nil, err
	}
	found, err := s.finder.FindByIDUnscoped(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	return s.open(ctx, found)
}

func (s *ContentService) OpenForClient(ctx context.Context, clientID string, mediaID uuid.UUID) (*Content, error) {
	found, err := s.finder.FindByID(ctx, clientID, mediaID)
	if err != nil {
		return nil, err
	}
	return s.open(ctx, found)
}

func (s *ContentService) open(ctx context.Context, found *domain.Media) (*Content, error) {
	if found.Status != domain.MediaStatusStored || found.StorageKey == nil {
		return nil, domain.MediaNotReady()
	}
	body, err := s.storage.Open(ctx, *found.StorageKey)
	if errors.Is(err, ErrObjectNotFound) {
		return nil, domain.MediaNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("media: open stored object: %w", err)
	}
	return &Content{Media: *found, Body: body}, nil
}
