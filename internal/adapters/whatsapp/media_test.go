package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"

	"github.com/OBrunoW/catwire/internal/application/media"
)

func TestClassifyDownloadError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		gone      bool
		permanent bool
	}{
		{name: "nil", err: nil},
		{name: "404", err: fmt.Errorf("download: %w", whatsmeow.ErrMediaDownloadFailedWith404), gone: true},
		{name: "410", err: whatsmeow.ErrMediaDownloadFailedWith410, gone: true},
		{name: "no url", err: whatsmeow.ErrNoURLPresent, permanent: true},
		{name: "plaintext hash", err: whatsmeow.ErrInvalidMediaSHA256, permanent: true},
		{name: "hmac", err: whatsmeow.ErrInvalidMediaHMAC, permanent: true},
		{name: "transient", err: errors.New("timeout")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyDownloadError(test.err)

			if gotGone := errors.Is(classified, media.ErrMediaGone); gotGone != test.gone {
				t.Fatalf("gone = %v", gotGone)
			}
			if test.err == nil && classified != nil {
				t.Fatalf("classified = %v", classified)
			}
		})
	}
}

func TestMediaTypeForKind(t *testing.T) {
	expected := map[string]whatsmeow.MediaType{
		"image":    whatsmeow.MediaImage,
		"sticker":  whatsmeow.MediaImage,
		"video":    whatsmeow.MediaVideo,
		"audio":    whatsmeow.MediaAudio,
		"document": whatsmeow.MediaDocument,
	}
	for kind, mediaType := range expected {
		if got, known := mediaTypeForKind(kind); !known || got != mediaType {
			t.Fatalf("%s = %q %v", kind, got, known)
		}
	}
	if _, known := mediaTypeForKind("location"); known {
		t.Fatal("location must not be downloadable")
	}
}

func TestClientRegistryRemoveOnlyDropsSameClient(t *testing.T) {
	registry := NewClientRegistry()
	id := uuid.New()
	first, second := &whatsmeow.Client{}, &whatsmeow.Client{}
	registry.set(id, first)
	registry.set(id, second)

	registry.remove(id, first)
	kept, foundAfterStaleRemove := registry.get(id)
	registry.remove(id, second)
	_, foundAfterRemove := registry.get(id)

	if !foundAfterStaleRemove || kept != second || foundAfterRemove {
		t.Fatalf("kept = %p stale = %v removed = %v", kept, foundAfterStaleRemove, foundAfterRemove)
	}
}

func TestDownloadRequiresConnectedInstance(t *testing.T) {
	gateway := NewMediaGateway(NewClientRegistry())

	err := gateway.Download(context.Background(), media.DownloadRequest{InstanceID: uuid.New(), Kind: "image"}, nil)

	if err == nil {
		t.Fatal("expected error")
	}
	if err := gateway.RequestRetry(context.Background(), uuid.New(), media.RetryTarget{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestClassifyDownloadErrorDropsURLFromUnknownErrors(t *testing.T) {
	leaked := "https://mmg.whatsapp.net/v/t62/secret%2Fpath?token=tok-123"
	err := &url.Error{Op: "Get", URL: leaked, Err: errors.New("connection reset")}

	classified := classifyDownloadError(err)

	if strings.Contains(classified.Error(), "tok-123") || strings.Contains(classified.Error(), "secret") {
		t.Fatalf("classified error leaks url: %s", classified)
	}
	if classified.Error() != "download failed" {
		t.Fatalf("classified = %s", classified)
	}
}

func TestClassifyDownloadErrorKeepsOnlyStatusForGoneMedia(t *testing.T) {
	classified := classifyDownloadError(fmt.Errorf("get https://host/path?token=abc: %w", whatsmeow.ErrMediaDownloadFailedWith410))

	if !errors.Is(classified, media.ErrMediaGone) || strings.Contains(classified.Error(), "token") {
		t.Fatalf("classified = %v", classified)
	}
	if !strings.Contains(classified.Error(), "410") {
		t.Fatalf("status missing: %v", classified)
	}
}

func TestDownloadDisconnectedInstanceReturnsSentinel(t *testing.T) {
	gateway := NewMediaGateway(NewClientRegistry())

	err := gateway.Download(context.Background(), media.DownloadRequest{InstanceID: uuid.New(), Kind: "image"}, nil)

	if !errors.Is(err, media.ErrInstanceDisconnected) {
		t.Fatalf("err = %v", err)
	}
}
