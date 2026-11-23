package outbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

func (h *harness) newWorker() *Worker {
	return NewWorker(WorkerOptions{
		Unit:    h.db,
		Gateway: h.gateway,
		Storage: h.storage,
		MaxAge:  10 * time.Minute,
		Backoff: []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return h.now },
	})
}

func (h *harness) runWorker(t *testing.T, worker *Worker) {
	t.Helper()
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) accept(t *testing.T, text string) *domain.Message {
	t.Helper()
	result, err := h.send("", textRequest("5511988887777", text))
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range h.db.messages {
		if message.ID == result.Message.ID {
			return message
		}
	}
	t.Fatal("accepted message not stored")
	return nil
}

func TestWorkerSendsAndEmitsSentUsingThePreGeneratedID(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")

	h.runWorker(t, h.newWorker())

	if message.Status != domain.MessageStatusSent || message.SentAt == nil {
		t.Fatalf("expected sent message, got %+v", message)
	}
	if h.gateway.sendCount() != 1 || h.gateway.sent[0].WAMessageID != message.WAMessageID {
		t.Fatalf("worker must reuse the wa_message_id: %+v", h.gateway.sent)
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != ingestion.EventTypeMessageSent {
		t.Fatalf("expected one sent event, got %v", got)
	}
	event := h.db.outbox[0]
	var data ingestion.MessageEventData
	if err := json.Unmarshal(event.Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Message.Status != domain.MessageStatusSent || data.Message.WAMessageID != message.WAMessageID || data.Message.Origin != OriginAPI {
		t.Fatalf("unexpected event payload: %+v", data.Message)
	}
	if event.ClientID != "client" || event.PartitionKey != h.instance.ID.String()+":5511988887777@s.whatsapp.net" {
		t.Fatalf("unexpected event routing: %+v", event)
	}
}

func TestWorkerEmitsSentOnceWhenReceiptAdvancedBeforeMarkSent(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	h.gateway.afterSend = func() { message.Status = domain.MessageStatusDelivered }
	worker := h.newWorker()

	h.runWorker(t, worker)
	h.runWorker(t, worker)

	if got := h.db.eventTypes(); len(got) != 1 || got[0] != ingestion.EventTypeMessageSent {
		t.Fatalf("expected exactly one sent event, got %v", got)
	}
	if h.gateway.sendCount() != 1 {
		t.Fatalf("expected one send, got %d", h.gateway.sendCount())
	}
}

func TestWorkerDoesNotEmitSentAgainWhenSentAtIsAlreadySet(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	message.Status = domain.MessageStatusDelivered
	sentAt := h.now
	message.SentAt = &sentAt
	entry := queueEntry{instance: *h.instance, message: *message}

	if err := h.newWorker().markSent(context.Background(), entry, SendReceipt{Timestamp: h.now}); err != nil {
		t.Fatal(err)
	}

	if len(h.db.outbox) != 0 {
		t.Fatalf("expected no event, got %v", h.db.eventTypes())
	}
}

func TestWorkerDoesNotSendAgainOnceSent(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.accept(t, "hello")
	worker := h.newWorker()

	h.runWorker(t, worker)
	h.runWorker(t, worker)

	if h.gateway.sendCount() != 1 || len(h.db.outbox) != 1 {
		t.Fatalf("sends=%d events=%d", h.gateway.sendCount(), len(h.db.outbox))
	}
}

func TestWorkerSendsInCreationOrder(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first := h.accept(t, "one")
	second := h.accept(t, "two")
	third := h.accept(t, "three")

	h.runWorker(t, h.newWorker())

	order := []string{}
	for _, command := range h.gateway.sent {
		order = append(order, command.WAMessageID)
	}
	expected := []string{first.WAMessageID, second.WAMessageID, third.WAMessageID}
	if strings.Join(order, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected %v, got %v", expected, order)
	}
}

func TestWorkerTransientFailureSchedulesRetryAndHoldsFollowingMessages(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first := h.accept(t, "one")
	second := h.accept(t, "two")
	h.gateway.sendErrors = []error{errBoom}
	worker := h.newWorker()

	h.runWorker(t, worker)

	if first.Status != domain.MessageStatusPending || first.SendAttempts != 1 || first.NextAttemptAt == nil {
		t.Fatalf("expected scheduled retry, got %+v", first)
	}
	if !first.NextAttemptAt.Equal(h.now.Add(2 * time.Second)) {
		t.Fatalf("expected 2s backoff, got %v", first.NextAttemptAt.Sub(h.now))
	}
	if second.Status != domain.MessageStatusPending || h.gateway.sendCount() != 1 {
		t.Fatal("following messages must wait behind the retrying one")
	}

	h.runWorker(t, worker)
	if h.gateway.sendCount() != 1 {
		t.Fatal("must not retry before next_attempt_at")
	}

	h.now = h.now.Add(3 * time.Second)
	h.runWorker(t, worker)
	if first.Status != domain.MessageStatusSent || second.Status != domain.MessageStatusSent {
		t.Fatalf("expected both sent after the retry, got %s and %s", first.Status, second.Status)
	}
	if h.gateway.sent[1].WAMessageID != first.WAMessageID {
		t.Fatal("retry must reuse the same wa_message_id")
	}
	if got := h.db.eventTypes(); len(got) != 2 {
		t.Fatalf("expected two sent events, got %v", got)
	}
}

func TestWorkerFailsAfterRetriesAreExhausted(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	h.gateway.sendErrors = []error{errBoom, errBoom, errBoom, errBoom}
	worker := h.newWorker()

	for range 4 {
		h.runWorker(t, worker)
		h.now = h.now.Add(20 * time.Second)
	}

	if message.Status != domain.MessageStatusFailed || message.ErrorCode == nil || *message.ErrorCode != CodeSendFailed {
		t.Fatalf("expected failed message, got %+v", message)
	}
	if h.gateway.sendCount() != 4 {
		t.Fatalf("expected 4 sends, got %d", h.gateway.sendCount())
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != EventTypeMessageFailed {
		t.Fatalf("expected a single failed event, got %v", got)
	}
}

func TestWorkerPermanentFailureEmitsFailedWithoutRetry(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	h.gateway.sendErrors = []error{&PermanentError{Code: "RECIPIENT_NOT_ON_WHATSAPP", Message: "recipient has no whatsapp account"}}

	h.runWorker(t, h.newWorker())

	if message.Status != domain.MessageStatusFailed || *message.ErrorCode != "RECIPIENT_NOT_ON_WHATSAPP" {
		t.Fatalf("unexpected message: %+v", message)
	}
	var data ingestion.MessageEventData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if h.db.outbox[0].EventType != EventTypeMessageFailed || data.Message.Error == nil || data.Message.Error.Code != "RECIPIENT_NOT_ON_WHATSAPP" {
		t.Fatalf("unexpected failed event: %s %+v", h.db.outbox[0].EventType, data.Message.Error)
	}
	if h.gateway.sendCount() != 1 {
		t.Fatal("permanent failure must not retry")
	}
}

func TestWorkerExpiredMessageFailsWithoutSending(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	h.now = h.now.Add(11 * time.Minute)

	h.runWorker(t, h.newWorker())

	if h.gateway.sendCount() != 0 {
		t.Fatal("expired message must not be sent")
	}
	if message.Status != domain.MessageStatusFailed || *message.ErrorCode != CodeSendExpired {
		t.Fatalf("unexpected message: %+v", message)
	}
	if got := h.db.eventTypes(); len(got) != 1 || got[0] != EventTypeMessageFailed {
		t.Fatalf("expected failed event, got %v", got)
	}
}

func TestWorkerExpiredHeadDoesNotBlockFreshMessages(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	old := h.accept(t, "old")
	h.now = h.now.Add(11 * time.Minute)
	h.db.clock = h.now
	fresh := h.accept(t, "fresh")

	h.runWorker(t, h.newWorker())

	if old.Status != domain.MessageStatusFailed || fresh.Status != domain.MessageStatusSent {
		t.Fatalf("expected old failed and fresh sent, got %s and %s", old.Status, fresh.Status)
	}
}

func TestWorkerWaitsWhileInstanceIsDisconnected(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	h.db.instances[h.instance.ID].Status = domain.InstanceStatusDisconnected
	worker := h.newWorker()

	h.runWorker(t, worker)
	if h.gateway.sendCount() != 0 || message.SendAttempts != 0 || message.Status != domain.MessageStatusPending {
		t.Fatal("disconnected instance must keep the message pending without consuming attempts")
	}

	h.db.instances[h.instance.ID].Status = domain.InstanceStatusConnected
	h.runWorker(t, worker)
	if message.Status != domain.MessageStatusSent {
		t.Fatalf("expected sent after reconnection, got %s", message.Status)
	}
}

func TestWorkerKeepsMessagePendingWhenGatewayReportsDisconnection(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	message := h.accept(t, "hello")
	h.gateway.sendErrors = []error{ErrInstanceDisconnected}
	worker := h.newWorker()

	h.runWorker(t, worker)
	if message.Status != domain.MessageStatusPending || message.SendAttempts != 0 {
		t.Fatalf("unexpected message: %+v", message)
	}

	h.runWorker(t, worker)
	if message.Status != domain.MessageStatusSent {
		t.Fatalf("expected sent, got %s", message.Status)
	}
}

func TestWorkerSendsMediaFromStorage(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	encoded := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	caption := "look"
	if _, err := h.send("", SendRequest{
		To: "5511988887777", Type: MessageTypeImage, Text: &caption,
		Media: &MediaInput{Base64: encoded, MimeType: "image/png"},
	}); err != nil {
		t.Fatal(err)
	}

	h.runWorker(t, h.newWorker())

	command := h.gateway.sent[0]
	if command.Media == nil || command.Media.MimeType != "image/png" || string(h.gateway.sentBodies[0]) != "png-bytes" {
		t.Fatalf("unexpected media payload: %+v", command.Media)
	}
	if command.Text == nil || *command.Text != "look" {
		t.Fatal("caption must be sent")
	}
}

func TestWorkerFailsPermanentlyWhenMediaObjectIsMissing(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	if _, err := h.send("", SendRequest{
		To: "5511988887777", Type: MessageTypeImage,
		Media: &MediaInput{Base64: base64.StdEncoding.EncodeToString([]byte("x")), MimeType: "image/png"},
	}); err != nil {
		t.Fatal(err)
	}
	h.storage.objects = map[string][]byte{}

	h.runWorker(t, h.newWorker())

	if h.db.messages[0].Status != domain.MessageStatusFailed || *h.db.messages[0].ErrorCode != "MEDIA_UNAVAILABLE" {
		t.Fatalf("unexpected message: %+v", h.db.messages[0])
	}
}

func TestWorkerSendsReplyReference(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	first := h.accept(t, "first")
	reply := textRequest("5511988887777", "reply")
	reply.ReplyToMessageID = &first.ID
	if _, err := h.send("", reply); err != nil {
		t.Fatal(err)
	}

	h.runWorker(t, h.newWorker())

	command := h.gateway.sent[1]
	if command.Reply == nil || command.Reply.WAMessageID != first.WAMessageID || command.Reply.Text == nil || *command.Reply.Text != "first" {
		t.Fatalf("unexpected reply reference: %+v", command.Reply)
	}
}

func TestEventsDoNotLeakRequestBodyOrIdempotencyData(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	encoded := base64.StdEncoding.EncodeToString([]byte("secret-image-bytes"))
	if _, err := h.send("idempotency-secret", SendRequest{
		To: "5511988887777", Type: MessageTypeImage,
		Media:    &MediaInput{Base64: encoded, MimeType: "image/png"},
		Metadata: json.RawMessage(`{"conversation_id":"c-981"}`),
	}); err != nil {
		t.Fatal(err)
	}
	hash := *h.db.messages[0].RequestHash

	h.runWorker(t, h.newWorker())

	payload := string(h.db.outbox[0].Payload)
	for _, forbidden := range []string{encoded, "secret-image-bytes", "idempotency-secret", hash, "storage_key", "request_hash", "idempotency_key"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("event payload leaks %q: %s", forbidden, payload)
		}
	}
	var data ingestion.MessageEventData
	if err := json.Unmarshal(h.db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Message.Media == nil || data.Message.Media.Status != domain.MediaStatusStored || data.Message.Media.DownloadURL != nil {
		t.Fatalf("event must carry the media resource: %+v", data.Message.Media)
	}
	if string(data.Message.Metadata) != `{"conversation_id":"c-981"}` {
		t.Fatalf("metadata must be echoed for correlation, got %s", data.Message.Metadata)
	}
}

func TestFailedEventErrorMessageIsFixedText(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	h.accept(t, "hello")
	h.gateway.sendErrors = []error{errBoom, errBoom, errBoom, errBoom}
	worker := h.newWorker()

	for range 4 {
		h.runWorker(t, worker)
		h.now = h.now.Add(20 * time.Second)
	}

	if strings.Contains(string(h.db.outbox[0].Payload), "boom") {
		t.Fatal("raw gateway error must not reach the event")
	}
}

func TestWorkerRunStopsOnCancel(t *testing.T) {
	h := newHarness(t, domain.InstanceStatusConnected)
	worker := h.newWorker()
	worker.PollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}
