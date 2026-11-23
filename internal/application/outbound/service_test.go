package outbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var testClock = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	db        *fakeDB
	gateway   *fakeGateway
	fetcher   *fakeFetcher
	storage   *fakeStorage
	converter *fakeConverter
	service   *Service
	instance  *domain.Instance
	now       time.Time
}

func newHarness(t *testing.T, status string) *harness {
	t.Helper()
	h := &harness{
		db:        newFakeDB(testClock),
		gateway:   newFakeGateway(),
		fetcher:   &fakeFetcher{content: []byte("fetched-image"), contentType: "image/png"},
		storage:   newFakeStorage(),
		converter: &fakeConverter{},
		now:       testClock,
	}
	h.instance = h.db.addInstance(status)
	h.gateway.register("5511988887777")
	h.service = NewService(ServiceOptions{
		Unit:          h.db,
		Gateway:       h.gateway,
		Fetcher:       h.fetcher,
		Storage:       h.storage,
		Converter:     h.converter,
		MaxMediaBytes: 1024,
		TempDir:       t.TempDir(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return h.now },
	})
	return h
}

func (h *harness) send(key string, request SendRequest) (SendResult, error) {
	return h.service.Send(context.Background(), "client", h.instance.ID, key, request)
}

func textRequest(to, text string) SendRequest {
	return SendRequest{To: to, Type: MessageTypeText, Text: &text}
}

func assertCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	appError, ok := domain.AsAppError(err)
	if !ok {
		t.Fatalf("expected app error %s, got %v", code, err)
	}
	if appError.Status != status || appError.Code != code {
		t.Fatalf("expected %d %s, got %d %s", status, code, appError.Status, appError.Code)
	}
}

func TestSendAcceptsTextAsPendingWithPreGeneratedID(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)

	result, err := h.send("", textRequest("5511988887777", "hello"))

	if err != nil {
		t.Fatal(err)
	}
	message := result.Message
	if message.Status != domain.MessageStatusPending || message.WAMessageID == "" || message.Origin != OriginAPI {
		t.Fatalf("unexpected message: %+v", message)
	}
	if result.Replayed || result.ChatJID != "5511988887777@s.whatsapp.net" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(h.db.outbox) != 0 || h.gateway.sendCount() != 0 {
		t.Fatal("accepting must not send or emit")
	}
}

func TestSendToExistingChatByIDSkipsRecipientLookup(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	chat := domain.Chat{ID: uuid.New(), InstanceID: h.instance.ID, JID: "120363@g.us", Type: domain.ChatTypeGroup}
	h.db.chats = append(h.db.chats, &chat)
	text := "group hello"

	result, err := h.send("", SendRequest{ChatID: &chat.ID, Type: MessageTypeText, Text: &text})

	if err != nil {
		t.Fatal(err)
	}
	if len(h.gateway.lookups) != 0 || result.Message.ChatID != chat.ID {
		t.Fatalf("lookups=%v chat=%s", h.gateway.lookups, result.Message.ChatID)
	}
}

func TestSendValidationRules(t *testing.T) {
	text := "hello"
	longText := strings.Repeat("a", MaxTextLength+1)
	chatID := uuid.New()
	mediaID := uuid.New()
	cases := map[string]SendRequest{
		"neither to nor chat_id":      {Type: MessageTypeText, Text: &text},
		"both to and chat_id":         {To: "5511988887777", ChatID: &chatID, Type: MessageTypeText, Text: &text},
		"invalid to":                  {To: "abc", Type: MessageTypeText, Text: &text},
		"group jid in to":             {To: "120363@g.us", Type: MessageTypeText, Text: &text},
		"unknown type":                {To: "5511988887777", Type: "sticker", Text: &text},
		"text without text":           {To: "5511988887777", Type: MessageTypeText},
		"text with media":             {To: "5511988887777", Type: MessageTypeText, Text: &text, Media: &MediaInput{URL: "https://x.test/a"}},
		"text too long":               {To: "5511988887777", Type: MessageTypeText, Text: &longText},
		"image without media":         {To: "5511988887777", Type: MessageTypeImage},
		"caption too long":            {To: "5511988887777", Type: MessageTypeImage, Text: &longText, Media: &MediaInput{URL: "https://x.test/a"}},
		"audio with text":             {To: "5511988887777", Type: MessageTypeAudio, Text: &text, Media: &MediaInput{URL: "https://x.test/a"}},
		"two media sources":           {To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{URL: "https://x.test/a", MediaID: &mediaID}},
		"no media source":             {To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{}},
		"base64 without mime":         {To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{Base64: "AAAA"}},
		"voice note on image":         {To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{URL: "https://x.test/a", AsVoiceNote: true}},
		"non http url":                {To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{URL: "file:///etc/passwd"}},
		"metadata not object":         {To: "5511988887777", Type: MessageTypeText, Text: &text, Metadata: json.RawMessage(`[1]`)},
		"metadata above 4 KB":         {To: "5511988887777", Type: MessageTypeText, Text: &text, Metadata: json.RawMessage(`{"k":"` + strings.Repeat("a", MaxMetadataBytes) + `"}`)},
		"lid with colon":              {To: "a:1@lid", Type: MessageTypeText, Text: &text},
		"phone with plus":             {To: "+5511988887777", Type: MessageTypeText, Text: &text},
		"phone shorter than minimum":  {To: "1234567", Type: MessageTypeText, Text: &text},
		"phone longer than maximum":   {To: "1234567890123456", Type: MessageTypeText, Text: &text},
		"media on audio with caption": {To: "5511988887777", Type: MessageTypeAudio, Text: &text, Media: &MediaInput{Base64: "AAAA", MimeType: "audio/ogg"}},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, domain.InstanceStatusConnected)

			_, err := h.send("", request)

			assertCode(t, err, 400, "BAD_REQUEST")
			if len(h.db.messages) != 0 {
				t.Fatal("invalid request must not create a message")
			}
		})
	}
}

func TestSendRejectsIdempotencyKeyAbove200Characters(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)

	_, err := h.send(strings.Repeat("k", 201), textRequest("5511988887777", "hello"))

	assertCode(t, err, 400, "BAD_REQUEST")
}

func TestSendValidRequestsPassValidation(t *testing.T) {
	text := "caption"
	mediaID := uuid.New()
	valid := []SendRequest{
		{To: "5511988887777@s.whatsapp.net", Type: MessageTypeText, Text: &text},
		{To: "abc123@lid", Type: MessageTypeText, Text: &text},
		{To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{URL: "https://x.test/a.png"}},
		{To: "5511988887777", Type: MessageTypeVideo, Text: &text, Media: &MediaInput{MediaID: &mediaID}},
		{To: "5511988887777", Type: MessageTypeAudio, Media: &MediaInput{Base64: "AAAA", MimeType: "audio/ogg", AsVoiceNote: true}},
		{To: "5511988887777", Type: MessageTypeDocument, Text: &text, Media: &MediaInput{URL: "https://x.test/a.pdf", FileName: "a.pdf"}, Metadata: json.RawMessage(`{"a":1}`)},
	}
	for _, request := range valid {
		if err := request.Validate(); err != nil {
			t.Fatalf("expected valid request %+v, got %v", request, err)
		}
	}
}

func TestSendRequiresConnectedInstance(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusDisconnected)

	_, err := h.send("", textRequest("5511988887777", "hello"))

	assertCode(t, err, 409, "INSTANCE_NOT_CONNECTED")
}

func TestSendUnknownInstance(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)

	_, err := h.service.Send(context.Background(), "client", uuid.New(), "", textRequest("5511988887777", "hello"))

	assertCode(t, err, 404, "INSTANCE_NOT_FOUND")
}

func TestSendRecipientNotOnWhatsApp(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)

	_, err := h.send("", textRequest("5511977776666", "hello"))

	assertCode(t, err, 422, "RECIPIENT_NOT_ON_WHATSAPP")
	if len(h.db.messages) != 0 {
		t.Fatal("no message for a missing recipient")
	}
}

func TestSendUsesCanonicalJIDFromLookup(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.gateway.registrations["5511988887777"] = Registration{Exists: true, JID: "551188887777@s.whatsapp.net"}

	result, err := h.send("", textRequest("5511988887777", "hello"))

	if err != nil {
		t.Fatal(err)
	}
	if result.ChatJID != "551188887777@s.whatsapp.net" {
		t.Fatalf("expected canonical jid, got %s", result.ChatJID)
	}
}

func TestSendMapsDisconnectedLookupTo409(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.gateway.lookupErr = ErrInstanceDisconnected

	_, err := h.send("", textRequest("5511988887777", "hello"))

	assertCode(t, err, 409, "INSTANCE_NOT_CONNECTED")
}

func TestSendMapsLookupFailureTo502(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.gateway.lookupErr = errBoom

	_, err := h.send("", textRequest("5511988887777", "hello"))

	assertCode(t, err, 502, "WHATSAPP_ERROR")
}

func TestRecipientLookupIsCachedPerNumber(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)

	for range 3 {
		if _, err := h.send("", textRequest("5511988887777", "hello")); err != nil {
			t.Fatal(err)
		}
	}

	if len(h.gateway.lookups) != 1 {
		t.Fatalf("expected one lookup, got %v", h.gateway.lookups)
	}
}

func TestRecipientCacheExpires(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	if _, err := h.send("", textRequest("5511988887777", "one")); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(25 * time.Hour)

	if _, err := h.send("", textRequest("5511988887777", "two")); err != nil {
		t.Fatal(err)
	}

	if len(h.gateway.lookups) != 2 {
		t.Fatalf("expected a second lookup after expiry, got %v", h.gateway.lookups)
	}
}

func TestUnregisteredRecipientIsCachedBriefly(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	_, _ = h.send("", textRequest("5511977776666", "one"))
	_, _ = h.send("", textRequest("5511977776666", "two"))
	if len(h.gateway.lookups) != 1 {
		t.Fatalf("expected negative result cached, got %v", h.gateway.lookups)
	}
	h.now = h.now.Add(6 * time.Minute)
	h.gateway.register("5511977776666")

	if _, err := h.send("", textRequest("5511977776666", "three")); err != nil {
		t.Fatal(err)
	}
	if len(h.gateway.lookups) != 2 {
		t.Fatalf("expected negative cache to expire, got %v", h.gateway.lookups)
	}
}

func TestIdempotencySameBodyReturnsExistingMessage(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first, err := h.send("key-1", textRequest("5511988887777", "hello"))
	if err != nil {
		t.Fatal(err)
	}

	second, err := h.send("key-1", textRequest("5511988887777", "hello"))

	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.Message.ID != first.Message.ID || len(h.db.messages) != 1 {
		t.Fatalf("expected replay of %s, got %+v with %d messages", first.Message.ID, second, len(h.db.messages))
	}
}

func TestIdempotencyReplayReflectsCurrentStatus(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first, _ := h.send("key-1", textRequest("5511988887777", "hello"))
	h.db.messages[0].Status = domain.MessageStatusDelivered

	second, err := h.send("key-1", textRequest("5511988887777", "hello"))

	if err != nil || second.Message.ID != first.Message.ID || second.Message.Status != domain.MessageStatusDelivered {
		t.Fatalf("unexpected replay: %+v err=%v", second.Message, err)
	}
}

func TestIdempotencyReplayWorksWhenInstanceDisconnected(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first, _ := h.send("key-1", textRequest("5511988887777", "hello"))
	h.db.instances[h.instance.ID].Status = domain.InstanceStatusDisconnected

	second, err := h.send("key-1", textRequest("5511988887777", "hello"))

	if err != nil || second.Message.ID != first.Message.ID {
		t.Fatalf("unexpected replay: %+v err=%v", second.Message, err)
	}
}

func TestIdempotencyDifferentBodyConflicts(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	if _, err := h.send("key-1", textRequest("5511988887777", "hello")); err != nil {
		t.Fatal(err)
	}

	_, err := h.send("key-1", textRequest("5511988887777", "different"))

	assertCode(t, err, 409, "IDEMPOTENCY_CONFLICT")
	if len(h.db.messages) != 1 {
		t.Fatal("conflicting request must not create a message")
	}
}

func TestIdempotencyDifferentMetadataConflicts(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first := textRequest("5511988887777", "hello")
	first.Metadata = json.RawMessage(`{"a":1}`)
	second := first
	second.Metadata = json.RawMessage(`{"a":2}`)
	if _, err := h.send("key-1", first); err != nil {
		t.Fatal(err)
	}

	_, err := h.send("key-1", second)

	assertCode(t, err, 409, "IDEMPOTENCY_CONFLICT")
}

func TestIdempotencyIgnoresMetadataKeyOrderAndSpacing(t *testing.T) {
	first := textRequest("5511988887777", "hello")
	first.Metadata = json.RawMessage(`{"a":1,"b":2}`)
	second := first
	second.Metadata = json.RawMessage(`{ "b": 2, "a": 1 }`)

	firstHash, _ := first.Hash()
	secondHash, _ := second.Hash()

	if firstHash != secondHash {
		t.Fatal("equivalent metadata must hash equally")
	}
}

func TestIdempotencyKeyIsScopedPerInstance(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	other := h.db.addInstance(domain.InstanceStatusConnected)
	if _, err := h.send("key-1", textRequest("5511988887777", "hello")); err != nil {
		t.Fatal(err)
	}

	result, err := h.service.Send(context.Background(), "client", other.ID, "key-1", textRequest("5511988887777", "hello"))

	if err != nil || result.Replayed || len(h.db.messages) != 2 {
		t.Fatalf("expected a second message on the other instance, err=%v replayed=%v", err, result.Replayed)
	}
}

func TestConcurrentRequestsWithSameKeyCreateOneMessage(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.gateway.lookupBarrier = newBarrier(2)
	results := make([]SendResult, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index], errs[index] = h.send("key-1", textRequest("5511988887777", "hello"))
		}()
	}
	group.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if len(h.db.messages) != 1 {
		t.Fatalf("expected one message, got %d", len(h.db.messages))
	}
	if results[0].Message.ID != results[1].Message.ID || results[0].Replayed == results[1].Replayed {
		t.Fatalf("expected one created and one replayed result: %+v %+v", results[0].Replayed, results[1].Replayed)
	}
}

func TestConcurrentRequestsWithSameKeyAndDifferentBodyConflict(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.gateway.lookupBarrier = newBarrier(2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index, text := range []string{"one", "two"} {
		group.Add(1)
		go func() {
			defer group.Done()
			_, errs[index] = h.send("key-1", textRequest("5511988887777", text))
		}()
	}
	group.Wait()

	failures := 0
	for _, err := range errs {
		if err != nil {
			assertCode(t, err, 409, "IDEMPOTENCY_CONFLICT")
			failures++
		}
	}
	if failures != 1 || len(h.db.messages) != 1 {
		t.Fatalf("expected one conflict and one message, got %d conflicts and %d messages", failures, len(h.db.messages))
	}
}

func TestReplyMustBelongToTheSameChat(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first, _ := h.send("", textRequest("5511988887777", "first"))
	h.gateway.register("5511955554444")
	otherChat, _ := h.send("", textRequest("5511955554444", "other"))
	reply := textRequest("5511988887777", "reply")

	reply.ReplyToMessageID = &first.Message.ID
	accepted, err := h.send("", reply)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Message.ReplyToWAMessageID == nil || *accepted.Message.ReplyToWAMessageID != first.Message.WAMessageID {
		t.Fatal("reply must store the target wa_message_id")
	}
	if accepted.ReplyTarget == nil || *accepted.ReplyTarget != first.Message.ID {
		t.Fatal("reply target must be resolved")
	}

	reply.ReplyToMessageID = &otherChat.Message.ID
	_, err = h.send("", reply)
	assertCode(t, err, 404, "MESSAGE_NOT_FOUND")

	missing := uuid.New()
	reply.ReplyToMessageID = &missing
	_, err = h.send("", reply)
	assertCode(t, err, 404, "MESSAGE_NOT_FOUND")
}

func TestSendDocumentFromURLStoresAndAttachesMedia(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.fetcher.contentType = "application/pdf"
	caption := "contract"
	request := SendRequest{
		To: "5511988887777", Type: MessageTypeDocument, Text: &caption,
		Media: &MediaInput{URL: "https://files.test/contract.pdf?token=secret", FileName: "contract.pdf"},
	}

	result, err := h.send("", request)

	if err != nil {
		t.Fatal(err)
	}
	item := result.Media
	if item == nil || item.Status != domain.MediaStatusStored || item.MessageID == nil || *item.MessageID != result.Message.ID {
		t.Fatalf("media must be stored and attached: %+v", item)
	}
	if item.ExpiresAt != nil || item.FileName == nil || *item.FileName != "contract.pdf" || *item.MimeType != "application/pdf" {
		t.Fatalf("unexpected media: %+v", item)
	}
	if len(h.storage.objects) != 1 {
		t.Fatal("media content must be stored")
	}
}

func TestSendMediaFromBase64(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	encoded := base64.StdEncoding.EncodeToString([]byte("png-bytes"))

	result, err := h.send("", SendRequest{
		To: "5511988887777", Type: MessageTypeImage,
		Media: &MediaInput{Base64: encoded, MimeType: "image/png"},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.Media == nil || *result.Media.SizeBytes != int64(len("png-bytes")) || h.fetcher.calls != 0 {
		t.Fatalf("unexpected media: %+v", result.Media)
	}
}

func TestSendMediaFailures(t *testing.T) {
	cases := map[string]struct {
		prepare func(h *harness)
		media   MediaInput
		status  int
		code    string
	}{
		"url fetch fails": {
			prepare: func(h *harness) { h.fetcher.err = errBoom },
			media:   MediaInput{URL: "https://files.test/a.png"},
			status:  422, code: "MEDIA_FETCH_FAILED",
		},
		"invalid base64": {
			media:  MediaInput{Base64: "***not-base64***", MimeType: "image/png"},
			status: 400, code: "BAD_REQUEST",
		},
		"empty content": {
			prepare: func(h *harness) { h.fetcher.content = nil },
			media:   MediaInput{URL: "https://files.test/a.png"},
			status:  422, code: "MEDIA_FETCH_FAILED",
		},
		"content above limit": {
			prepare: func(h *harness) { h.fetcher.content = make([]byte, 2048) },
			media:   MediaInput{URL: "https://files.test/a.png"},
			status:  413, code: "MEDIA_TOO_LARGE",
		},
		"mime does not match type": {
			prepare: func(h *harness) { h.fetcher.contentType = "application/pdf" },
			media:   MediaInput{URL: "https://files.test/a.pdf"},
			status:  415, code: "UNSUPPORTED_MEDIA_TYPE",
		},
		"unknown media id": {
			media:  MediaInput{MediaID: ptr(uuid.New())},
			status: 404, code: "MEDIA_NOT_FOUND",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, domain.InstanceStatusConnected)
			if testCase.prepare != nil {
				testCase.prepare(h)
			}

			_, err := h.send("", SendRequest{To: "5511988887777", Type: MessageTypeImage, Media: &testCase.media})

			assertCode(t, err, testCase.status, testCase.code)
			if len(h.db.messages) != 0 {
				t.Fatal("failed media must not create a message")
			}
		})
	}
}

func ptr[T any](value T) *T {
	return &value
}

func (h *harness) addStoredMedia(kind string, mutate func(item *domain.Media)) *domain.Media {
	key := h.instance.ID.String() + "/stored"
	size := int64(6)
	mimeType := "image/png"
	storageBackend := "local"
	status := domain.MediaStatusStored
	item := &domain.Media{
		ID: uuid.New(), InstanceID: h.instance.ID, Kind: kind, Status: status, MimeType: &mimeType,
		SizeBytes: &size, StorageBackend: &storageBackend, StorageKey: &key,
	}
	if mutate != nil {
		mutate(item)
	}
	h.db.media = append(h.db.media, item)
	h.storage.objects[key] = []byte("stored")
	return item
}

func TestSendWithStoredMediaAttachesOnce(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	item := h.addStoredMedia(MessageTypeImage, nil)
	request := SendRequest{To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{MediaID: &item.ID}}

	result, err := h.send("", request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Media == nil || result.Media.ID != item.ID {
		t.Fatalf("expected stored media to be attached: %+v", result.Media)
	}

	_, err = h.send("", request)
	assertCode(t, err, 404, "MEDIA_NOT_FOUND")
}

func TestSendWithStoredMediaRejectsUnusableMedia(t *testing.T) {
	cases := map[string]func(item *domain.Media){
		"pending": func(item *domain.Media) { item.Status = domain.MediaStatusPending },
		"other instance": func(item *domain.Media) {
			item.InstanceID = uuid.New()
		},
		"already attached": func(item *domain.Media) {
			attached := uuid.New()
			item.MessageID = &attached
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, domain.InstanceStatusConnected)
			item := h.addStoredMedia(MessageTypeImage, mutate)

			_, err := h.send("", SendRequest{To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{MediaID: &item.ID}})

			assertCode(t, err, 404, "MEDIA_NOT_FOUND")
		})
	}
}

func TestSendWithStoredMediaOfAnotherKindIsBadRequest(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	item := h.addStoredMedia(MessageTypeDocument, nil)

	_, err := h.send("", SendRequest{To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{MediaID: &item.ID}})

	assertCode(t, err, 400, "BAD_REQUEST")
}

func TestVoiceNoteIsConvertedAndFlagged(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	encoded := base64.StdEncoding.EncodeToString([]byte("mp3-bytes"))

	result, err := h.send("", SendRequest{
		To: "5511988887777", Type: MessageTypeAudio,
		Media: &MediaInput{Base64: encoded, MimeType: "audio/mpeg", AsVoiceNote: true},
	})

	if err != nil {
		t.Fatal(err)
	}
	if h.converter.calls != 1 || !result.Media.IsVoiceNote || *result.Media.MimeType != oggOpusMimeType {
		t.Fatalf("voice note not converted: calls=%d media=%+v", h.converter.calls, result.Media)
	}
}

func TestVoiceNoteFromStoredMediaCreatesConvertedCopy(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	item := h.addStoredMedia(MessageTypeAudio, func(item *domain.Media) {
		mimeType := "audio/mpeg"
		item.MimeType = &mimeType
	})

	result, err := h.send("", SendRequest{
		To: "5511988887777", Type: MessageTypeAudio,
		Media: &MediaInput{MediaID: &item.ID, AsVoiceNote: true},
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.Media.ID == item.ID || !result.Media.IsVoiceNote || h.converter.calls != 1 {
		t.Fatalf("expected a converted copy: %+v", result.Media)
	}
}

func TestVoiceNoteConversionFailureIs422(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.converter.err = errBoom

	_, err := h.send("", SendRequest{
		To: "5511988887777", Type: MessageTypeAudio,
		Media: &MediaInput{Base64: base64.StdEncoding.EncodeToString([]byte("x")), MimeType: "audio/mpeg", AsVoiceNote: true},
	})

	assertCode(t, err, 422, "MEDIA_FETCH_FAILED")
}

func TestErrorsDoNotExposeMediaURL(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.fetcher.err = errors.New("get https://files.test/a.png?token=secret failed")

	_, err := h.send("", SendRequest{To: "5511988887777", Type: MessageTypeImage, Media: &MediaInput{URL: "https://files.test/a.png?token=secret"}})

	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error must not expose the url: %v", err)
	}
}
