package media

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

type harness struct {
	db         *fakeDB
	storage    *fakeStorage
	downloader *fakeDownloader
	converter  *fakeConverter
	retries    *fakeRetries
	worker     *Worker
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	signer, err := NewSigner("secret", func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		db:         newFakeDB(),
		storage:    newFakeStorage(),
		downloader: &fakeDownloader{content: []byte("0123456789")},
		converter:  &fakeConverter{},
		retries:    &fakeRetries{},
	}
	h.worker = NewWorker(Options{
		Unit:          h.db,
		Storage:       h.storage,
		Downloader:    h.downloader,
		Converter:     h.converter,
		Retries:       h.retries,
		Signer:        signer,
		PublicBaseURL: "https://catwire.test",
		MaxBytes:      100,
		MaxAttempts:   3,
		TempDir:       t.TempDir(),
		Now:           func() time.Time { return fixedNow },
	})
	return h
}

func (h *harness) run(t *testing.T) {
	t.Helper()
	if _, err := h.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerStoresMediaAndEmitsStoredEvent(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.CreatedAt = time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)

	h.run(t)

	if item.Status != domain.MediaStatusStored {
		t.Fatalf("status = %s", item.Status)
	}
	expectedKey := fmt.Sprintf("%s/2026/10/%s", item.InstanceID, item.ID)
	if item.StorageKey == nil || *item.StorageKey != expectedKey {
		t.Fatalf("storage key = %v, want %s", item.StorageKey, expectedKey)
	}
	if string(h.storage.objects[expectedKey]) != "0123456789" {
		t.Fatalf("stored content = %q", h.storage.objects[expectedKey])
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != EventTypeMediaStored {
		t.Fatalf("events = %v", got)
	}
	var data MediaEventData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Media.Status != domain.MediaStatusStored || data.Media.DownloadURL == nil || data.WAMessageID != "3EB0AA" {
		t.Fatalf("unexpected payload %+v", data)
	}
	if !strings.HasPrefix(*data.Media.DownloadURL, "https://catwire.test/api/v1/media/"+item.ID.String()+"/content?") {
		t.Fatalf("download url = %s", *data.Media.DownloadURL)
	}
	if want := item.InstanceID.String() + ":5511999999999@s.whatsapp.net"; h.db.outbox[0].PartitionKey != want {
		t.Fatalf("partition key = %s", h.db.outbox[0].PartitionKey)
	}
}

func TestWorkerFailureSchedulesBackoff(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	h.downloader.err = errBoom

	h.run(t)

	if item.Status != domain.MediaStatusPending || item.DownloadAttempts != 1 {
		t.Fatalf("status = %s attempts = %d", item.Status, item.DownloadAttempts)
	}
	if got := h.db.failures[item.ID]; !got.Equal(fixedNow.Add(30 * time.Second)) {
		t.Fatalf("next attempt = %v", got)
	}
	if len(h.db.outbox) != 0 {
		t.Fatalf("unexpected events %v", h.db.eventTypes())
	}
}

func TestWorkerBackoffGrowsWithAttempts(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.DownloadAttempts = 1
	h.downloader.err = errBoom

	h.run(t)

	if got := h.db.failures[item.ID]; !got.Equal(fixedNow.Add(2 * time.Minute)) {
		t.Fatalf("next attempt = %v", got)
	}
}

func TestWorkerExhaustedAttemptsEmitsFailedEvent(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.DownloadAttempts = 2
	h.downloader.err = errBoom

	h.run(t)

	if item.Status != domain.MediaStatusFailed {
		t.Fatalf("status = %s", item.Status)
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != EventTypeMediaFailed {
		t.Fatalf("events = %v", got)
	}
	var data MediaFailedData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Error.Code != CodeMediaDownloadFailed || data.Media.Status != domain.MediaStatusFailed {
		t.Fatalf("unexpected payload %+v", data)
	}
}

func TestWorkerRejectsDownloadAboveMaxBytes(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.SizeBytes = nil
	h.worker.MaxBytes = 5

	h.run(t)

	if item.Status != domain.MediaStatusFailed || len(h.storage.puts) != 0 {
		t.Fatalf("status = %s puts = %v", item.Status, h.storage.puts)
	}
	var data MediaFailedData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Error.Code != CodeMediaTooLarge {
		t.Fatalf("code = %s", data.Error.Code)
	}
}

func TestWorkerSkipsDownloadWhenDeclaredSizeAboveMaxBytes(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	h.worker.MaxBytes = 5

	h.run(t)

	if item.Status != domain.MediaStatusFailed || len(h.downloader.requests) != 0 {
		t.Fatalf("status = %s downloads = %d", item.Status, len(h.downloader.requests))
	}
}

func TestWorkerFailsWithoutDownloadCredentials(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.WADirectPath = nil

	h.run(t)

	if item.Status != domain.MediaStatusFailed || len(h.downloader.requests) != 0 {
		t.Fatalf("status = %s", item.Status)
	}
}

func TestWorkerConvertsVoiceNoteToOggOpus(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.Kind = "audio"
	item.IsVoiceNote = true
	mimeType := "audio/mp4"
	item.MimeType = &mimeType

	h.run(t)

	if h.converter.calls != 1 {
		t.Fatalf("converter calls = %d", h.converter.calls)
	}
	if item.MimeType == nil || *item.MimeType != oggOpusMimeType {
		t.Fatalf("mime = %v", item.MimeType)
	}
	if !strings.HasPrefix(string(h.storage.objects[*item.StorageKey]), "OggS") {
		t.Fatal("converted content was not stored")
	}
}

func TestWorkerKeepsCompatibleVoiceNote(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		voiceNote bool
		mimeType  string
	}{
		{name: "ogg voice note", kind: "audio", voiceNote: true, mimeType: "audio/ogg; codecs=opus"},
		{name: "plain audio", kind: "audio", voiceNote: false, mimeType: "audio/mp4"},
		{name: "image", kind: "image", voiceNote: false, mimeType: "image/jpeg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			item := h.db.seed()
			item.Kind = test.kind
			item.IsVoiceNote = test.voiceNote
			item.MimeType = &test.mimeType

			h.run(t)

			if h.converter.calls != 0 || item.Status != domain.MediaStatusStored {
				t.Fatalf("converter calls = %d status = %s", h.converter.calls, item.Status)
			}
		})
	}
}

func TestWorkerConversionFailureSchedulesRetry(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.Kind = "audio"
	item.IsVoiceNote = true
	h.converter.err = errBoom

	h.run(t)

	if item.Status != domain.MediaStatusPending || len(h.storage.puts) != 0 {
		t.Fatalf("status = %s", item.Status)
	}
}

func TestWorkerGoneMediaRequestsRetryFromPhone(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	h.downloader.err = fmt.Errorf("%w: 404", ErrMediaGone)

	h.run(t)

	if len(h.retries.targets) != 1 {
		t.Fatalf("retry requests = %d", len(h.retries.targets))
	}
	target := h.retries.targets[0]
	if target.WAMessageID != "3EB0AA" || target.ChatJID != "5511999999999@s.whatsapp.net" || string(target.MediaKey) != "media-key" {
		t.Fatalf("unexpected target %+v", target)
	}
	if item.Status != domain.MediaStatusPending || item.LastError == nil || *item.LastError != retryRequestedMessage {
		t.Fatalf("status = %s last error = %v", item.Status, item.LastError)
	}
	if got := h.db.failures[item.ID]; !got.Equal(fixedNow.Add(defaultRetryWait)) {
		t.Fatalf("next attempt = %v", got)
	}
	if len(h.db.outbox) != 0 {
		t.Fatalf("unexpected events %v", h.db.eventTypes())
	}
}

func TestWorkerGoneMediaWithoutRetryRequesterFailsAfterBackoffs(t *testing.T) {
	h := newHarness(t)
	h.worker.Retries = nil
	item := h.db.seed()
	item.DownloadAttempts = 2
	h.downloader.err = fmt.Errorf("%w: 410", ErrMediaGone)

	h.run(t)

	if item.Status != domain.MediaStatusFailed {
		t.Fatalf("status = %s", item.Status)
	}
	var data MediaFailedData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Error.Code != CodeMediaUnavailable {
		t.Fatalf("code = %s", data.Error.Code)
	}
}

func TestWorkerRetryRequestFailureFallsBackToBackoff(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	h.downloader.err = fmt.Errorf("%w: 404", ErrMediaGone)
	h.retries.err = errBoom

	h.run(t)

	if item.LastError == nil || *item.LastError == retryRequestedMessage {
		t.Fatalf("last error = %v", item.LastError)
	}
	if got := h.db.failures[item.ID]; !got.Equal(fixedNow.Add(30 * time.Second)) {
		t.Fatalf("next attempt = %v", got)
	}
}

func TestWorkerFailsWhenRetryRequestGotNoResponse(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	marker := retryRequestedMessage
	item.LastError = &marker

	h.run(t)

	if item.Status != domain.MediaStatusFailed || len(h.downloader.requests) != 0 {
		t.Fatalf("status = %s downloads = %d", item.Status, len(h.downloader.requests))
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != EventTypeMediaFailed {
		t.Fatalf("events = %v", got)
	}
}

func TestHandleRetryAppliesNewDirectPath(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	marker := retryRequestedMessage
	item.LastError = &marker

	err := h.worker.HandleRetry(context.Background(), item.InstanceID, "3EB0AA", func(mediaKey []byte) (RetryResult, error) {
		if string(mediaKey) != "media-key" {
			t.Fatalf("media key = %q", mediaKey)
		}
		return RetryResult{DirectPath: "/v/t62/fresh"}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if h.db.retryPath[item.ID] != "/v/t62/fresh" || item.LastError != nil || item.Status != domain.MediaStatusPending {
		t.Fatalf("path = %q last error = %v status = %s", h.db.retryPath[item.ID], item.LastError, item.Status)
	}
}

func TestHandleRetryFailureResultFailsMedia(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	marker := retryRequestedMessage
	item.LastError = &marker

	err := h.worker.HandleRetry(context.Background(), item.InstanceID, "3EB0AA", func([]byte) (RetryResult, error) {
		return RetryResult{Failure: "retry result NOT_FOUND"}, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if item.Status != domain.MediaStatusFailed {
		t.Fatalf("status = %s", item.Status)
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != EventTypeMediaFailed {
		t.Fatalf("events = %v", got)
	}
}

func TestHandleRetryIgnoresMediaNotAwaitingRetry(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()

	err := h.worker.HandleRetry(context.Background(), item.InstanceID, "3EB0AA", func([]byte) (RetryResult, error) {
		t.Fatal("decrypt must not run")
		return RetryResult{}, nil
	})
	unknown := h.worker.HandleRetry(context.Background(), item.InstanceID, "unknown", func([]byte) (RetryResult, error) {
		t.Fatal("decrypt must not run")
		return RetryResult{}, nil
	})

	if err != nil || unknown != nil || len(h.db.retryPath) != 0 {
		t.Fatalf("err = %v unknown = %v", err, unknown)
	}
}

func TestEventPayloadsNeverCarryMediaSecrets(t *testing.T) {
	h := newHarness(t)
	stored := h.db.seed()
	failed := h.db.seed()
	failed.DownloadAttempts = 2
	h.downloader.err = nil
	h.run(t)
	h.downloader.err = fmt.Errorf("get %s: %w", *failed.WADirectPath, errBoom)
	failed.Status = domain.MediaStatusPending
	h.run(t)

	if stored.Status != domain.MediaStatusStored || len(h.db.outbox) < 2 {
		t.Fatalf("setup produced events %v", h.db.eventTypes())
	}
	forbidden := []string{"direct_path", "media_key", "file_enc_sha256", "file_sha256", "secret-path", "media-key"}
	for _, event := range h.db.outbox {
		for _, word := range forbidden {
			if strings.Contains(string(event.Payload), word) {
				t.Fatalf("%s payload leaks %q: %s", event.EventType, word, event.Payload)
			}
		}
	}
	if failed.LastError != nil && strings.Contains(*failed.LastError, "secret-path") {
		t.Fatalf("last error leaks direct path: %s", *failed.LastError)
	}
}

func TestStoredEventAlwaysFollowsReceivedForSameMessage(t *testing.T) {
	h := newHarness(t)
	h.db.seed()

	h.run(t)

	for _, event := range h.db.outbox {
		if event.EventType == "whatsapp.message.received" {
			t.Fatal("media worker must not emit message events")
		}
	}
}

func TestWorkerDisconnectedInstanceDoesNotConsumeAttempt(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.DownloadAttempts = 2
	h.downloader.err = fmt.Errorf("whatsapp: %w", ErrInstanceDisconnected)

	h.run(t)

	if item.Status != domain.MediaStatusPending || item.DownloadAttempts != 2 {
		t.Fatalf("status = %s attempts = %d", item.Status, item.DownloadAttempts)
	}
	if got := h.db.failures[item.ID]; !got.Equal(fixedNow.Add(time.Minute)) {
		t.Fatalf("next attempt = %v", got)
	}
	if len(h.db.outbox) != 0 {
		t.Fatalf("unexpected events %v", h.db.eventTypes())
	}
}

func TestWorkerStopsDownloadAtLimitBeforeWritingEverything(t *testing.T) {
	h := newHarness(t)
	item := h.db.seed()
	item.SizeBytes = nil
	chunked := &chunkedDownloader{chunks: 1000}
	h.worker.Downloader = chunked
	h.worker.MaxBytes = 25

	h.run(t)

	if chunked.written != 2 {
		t.Fatalf("chunks written = %d, want 2", chunked.written)
	}
	if item.Status != domain.MediaStatusFailed || len(h.storage.puts) != 0 {
		t.Fatalf("status = %s puts = %v", item.Status, h.storage.puts)
	}
	var data MediaFailedData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Error.Code != CodeMediaTooLarge {
		t.Fatalf("code = %s", data.Error.Code)
	}
}

func TestWorkerNeverLeaksDownloadURLSecrets(t *testing.T) {
	capture := &captureHandler{}
	h := newHarness(t)
	h.worker.Logger = slog.New(capture)
	item := h.db.seed()
	item.DownloadAttempts = 2
	token := "tok-9f8e7d6c"
	leaked := "https://mmg.whatsapp.net" + url.PathEscape(*item.WADirectPath) + "&token=" + token + "&hash=ZW5j"
	h.downloader.err = &url.Error{Op: "Get", URL: leaked, Err: fmt.Errorf("dial %s: %w", leaked, errBoom)}

	h.run(t)

	if item.Status != domain.MediaStatusFailed || item.LastError == nil {
		t.Fatalf("status = %s", item.Status)
	}
	surfaces := []string{*item.LastError, capture.output()}
	for _, event := range h.db.outbox {
		surfaces = append(surfaces, string(event.Payload))
	}
	for _, surface := range surfaces {
		for _, secret := range []string{token, "secret-path", "mmg.whatsapp.net", "ZW5j"} {
			if strings.Contains(surface, secret) {
				t.Fatalf("surface leaks %q: %s", secret, surface)
			}
		}
	}
	if !strings.Contains(capture.output(), "error_type") {
		t.Fatalf("log missing error type: %s", capture.output())
	}
}

func TestRedactCoversKeysAndHashes(t *testing.T) {
	item := domain.Media{
		WAMediaKey: []byte("media-key"), WAFileSHA256: []byte("sha-plain"), WAFileEncSHA256: []byte("sha-encrypted"),
	}
	for _, raw := range [][]byte{item.WAMediaKey, item.WAFileSHA256, item.WAFileEncSHA256} {
		encodings := []string{hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw), base64.URLEncoding.EncodeToString(raw)}
		for _, encoded := range encodings {
			if got := redact("value "+encoded+" end", item); strings.Contains(got, encoded) {
				t.Fatalf("redact kept %q: %s", encoded, got)
			}
		}
	}
}
