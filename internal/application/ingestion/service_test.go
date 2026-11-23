package ingestion

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var (
	baseTime     = time.Date(2026, 11, 22, 13, 5, 0, 0, time.UTC)
	testInstance = domain.Instance{ID: uuid.New(), ClientID: "godi"}
)

func newTestService(db *fakeDB) *Service {
	return NewService(Options{Unit: db, Settings: db, Now: func() time.Time { return baseTime }})
}

func pointer[T any](value T) *T {
	return &value
}

func directChat() ChatRef {
	return ChatRef{JID: "5511988887777@s.whatsapp.net", Phone: pointer("5511988887777")}
}

func textMessage(waMessageID string, fromMe bool) IncomingMessage {
	return IncomingMessage{
		WAMessageID: waMessageID,
		Chat:        directChat(),
		Sender:      Sender{JID: "5511988887777@s.whatsapp.net", Phone: pointer("5511988887777")},
		FromMe:      fromMe,
		Timestamp:   baseTime,
		Source:      SourceLive,
		Content:     Content{Type: MessageTypeText, Text: pointer("hello")},
	}
}

func decodeMessageEvent(t *testing.T, event domain.OutboxEvent) MessageResource {
	t.Helper()
	var data MessageEventData
	if err := json.Unmarshal(event.Payload, &data); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return data.Message
}

func TestIngestMessageRepeatedEventEmitsOnce(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	for range 3 {
		if err := service.IngestMessage(context.Background(), testInstance, textMessage("WA1", false)); err != nil {
			t.Fatal(err)
		}
	}
	if len(db.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(db.messages))
	}
	if types := db.eventTypes(); len(types) != 1 || types[0] != EventTypeMessageReceived {
		t.Fatalf("events = %v", types)
	}
	event := db.outbox[0]
	if event.ClientID != "godi" || event.EventID == uuid.Nil || event.EventVersion != 1 {
		t.Fatalf("event = %+v", event)
	}
	wantKey := testInstance.ID.String() + ":5511988887777@s.whatsapp.net"
	if event.PartitionKey != wantKey {
		t.Fatalf("partition key = %q, want %q", event.PartitionKey, wantKey)
	}
	resource := decodeMessageEvent(t, event)
	if resource.Direction != domain.DirectionInbound || resource.Origin != OriginContact || resource.Status != domain.MessageStatusReceived {
		t.Fatalf("resource = %+v", resource)
	}
}

func TestIngestMessageFromPhoneEmitsSentWithOriginDevice(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WA2", true)); err != nil {
		t.Fatal(err)
	}
	if types := db.eventTypes(); len(types) != 1 || types[0] != EventTypeMessageSent {
		t.Fatalf("events = %v", types)
	}
	stored := db.messages[0]
	if stored.Origin != OriginDevice || stored.Direction != domain.DirectionOutbound || stored.Status != domain.MessageStatusSent {
		t.Fatalf("message = %+v", stored)
	}
	if decodeMessageEvent(t, db.outbox[0]).Origin != OriginDevice {
		t.Fatal("event origin must be device")
	}
}

func TestIngestMessageEchoOfApiSendDoesNotEmit(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WA3", true)); err != nil {
		t.Fatal(err)
	}
	db.outbox = nil
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WA3", true)); err != nil {
		t.Fatal(err)
	}
	if len(db.outbox) != 0 || len(db.messages) != 1 {
		t.Fatalf("outbox = %d messages = %d", len(db.outbox), len(db.messages))
	}
}

func TestIngestReceiptEmitsOnlyWhenStatusAdvances(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WA4", true)); err != nil {
		t.Fatal(err)
	}
	db.outbox = nil
	receipt := ReceiptChange{Chat: directChat(), WAMessageIDs: []string{"WA4"}, Status: domain.MessageStatusDelivered, At: baseTime}
	for range 2 {
		if err := service.IngestReceipt(context.Background(), testInstance, receipt); err != nil {
			t.Fatal(err)
		}
	}
	if types := db.eventTypes(); len(types) != 1 || types[0] != EventTypeMessageStatusUpdated {
		t.Fatalf("events = %v", types)
	}
	var data StatusUpdatedData
	if err := json.Unmarshal(db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Status != domain.MessageStatusDelivered || data.PreviousStatus != domain.MessageStatusSent || data.Direction != domain.DirectionOutbound {
		t.Fatalf("data = %+v", data)
	}
	receipt.Status = domain.MessageStatusRead
	if err := service.IngestReceipt(context.Background(), testInstance, receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Status = domain.MessageStatusDelivered
	if err := service.IngestReceipt(context.Background(), testInstance, receipt); err != nil {
		t.Fatal(err)
	}
	if len(db.outbox) != 2 || db.messages[0].Status != domain.MessageStatusRead {
		t.Fatalf("outbox = %d status = %s", len(db.outbox), db.messages[0].Status)
	}
}

func groupMessage(waMessageID string) IncomingMessage {
	message := textMessage(waMessageID, false)
	message.Chat = ChatRef{JID: "120363000000000000@g.us", Group: true}
	return message
}

func TestIgnoreGroupsBlocksGroupMessages(t *testing.T) {
	db := newFakeDB()
	db.settings.IgnoreGroups = true
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, groupMessage("WG1")); err != nil {
		t.Fatal(err)
	}
	if len(db.messages) != 0 || len(db.chats) != 0 || len(db.outbox) != 0 {
		t.Fatalf("messages = %d chats = %d outbox = %d", len(db.messages), len(db.chats), len(db.outbox))
	}
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WD1", false)); err != nil {
		t.Fatal(err)
	}
	if len(db.messages) != 1 {
		t.Fatalf("direct messages must still be stored, got %d", len(db.messages))
	}
}

func TestGroupMessagesAreStoredAndEmittedByDefault(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, groupMessage("WG2")); err != nil {
		t.Fatal(err)
	}
	if len(db.messages) != 1 || len(db.outbox) != 1 || db.chats[0].Type != domain.ChatTypeGroup {
		t.Fatalf("messages = %d outbox = %d", len(db.messages), len(db.outbox))
	}
}

func historyBatch() HistoryBatch {
	first := textMessage("H1", false)
	second := textMessage("H2", true)
	other := textMessage("H3", false)
	other.Chat = ChatRef{JID: "5511977776666@s.whatsapp.net", Phone: pointer("5511977776666")}
	image := textMessage("H4", false)
	image.Content = Content{Type: MessageTypeImage, Media: &MediaDescriptor{Kind: "image"}}
	return HistoryBatch{
		SyncType:   "initial_bootstrap",
		ChunkOrder: 3,
		Progress:   42,
		Conversations: []HistoryConversation{
			{Chat: directChat(), Messages: []IncomingMessage{first, second, image}},
			{Chat: other.Chat, Messages: []IncomingMessage{other}},
		},
	}
}

func TestIngestHistoryEmitsOneSummaryAndNoMessageEvents(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestHistory(context.Background(), testInstance, historyBatch()); err != nil {
		t.Fatal(err)
	}
	if types := db.eventTypes(); len(types) != 1 || types[0] != EventTypeHistorySynced {
		t.Fatalf("events = %v", types)
	}
	if len(db.messages) != 4 {
		t.Fatalf("messages = %d", len(db.messages))
	}
	for _, message := range db.messages {
		if message.Source != SourceHistory {
			t.Fatalf("source = %s", message.Source)
		}
	}
	var data HistorySyncedData
	if err := json.Unmarshal(db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	want := HistorySyncedData{SyncType: "initial_bootstrap", ChunkOrder: 3, Progress: 42, Chats: 2, Messages: 4, InsertedMessages: 4}
	if data != want {
		t.Fatalf("data = %+v, want %+v", data, want)
	}
	if db.outbox[0].PartitionKey != testInstance.ID.String() {
		t.Fatalf("partition key = %q", db.outbox[0].PartitionKey)
	}
}

func TestIngestHistoryDoesNotDownloadMediaAndKeepsLiveContent(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("H1", false)); err != nil {
		t.Fatal(err)
	}
	db.outbox = nil
	if err := service.IngestHistory(context.Background(), testInstance, historyBatch()); err != nil {
		t.Fatal(err)
	}
	if len(db.media) != 1 || db.media[0].Status != domain.MediaStatusSkipped {
		t.Fatalf("media = %+v", db.media)
	}
	var data HistorySyncedData
	if err := json.Unmarshal(db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Messages != 4 || data.InsertedMessages != 3 {
		t.Fatalf("data = %+v", data)
	}
	if db.messages[0].Source != SourceLive {
		t.Fatal("live message must not be overwritten by history")
	}
}

func TestIngestHistoryHonorsIgnoreGroups(t *testing.T) {
	db := newFakeDB()
	db.settings.IgnoreGroups = true
	service := newTestService(db)
	batch := HistoryBatch{
		SyncType: "recent",
		Conversations: []HistoryConversation{
			{Chat: groupMessage("G1").Chat, Messages: []IncomingMessage{groupMessage("G1")}},
		},
	}
	if err := service.IngestHistory(context.Background(), testInstance, batch); err != nil {
		t.Fatal(err)
	}
	if len(db.messages) != 0 || len(db.chats) != 0 {
		t.Fatalf("messages = %d chats = %d", len(db.messages), len(db.chats))
	}
	var data HistorySyncedData
	if err := json.Unmarshal(db.outbox[0].Payload, &data); err != nil {
		t.Fatal(err)
	}
	if data.Chats != 0 || data.Messages != 0 {
		t.Fatalf("data = %+v", data)
	}
}

func imageMessage(waMessageID string) IncomingMessage {
	message := textMessage(waMessageID, false)
	message.Content = Content{
		Type:  MessageTypeImage,
		Text:  pointer("receipt"),
		Media: &MediaDescriptor{Kind: "image", MimeType: pointer("image/jpeg"), DirectPath: pointer("/v/t62/x")},
	}
	return message
}

func TestIngestMessageWithMediaCreatesPendingRowLinkedToMessage(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, imageMessage("WM1")); err != nil {
		t.Fatal(err)
	}
	if len(db.media) != 1 {
		t.Fatalf("media = %d", len(db.media))
	}
	media := db.media[0]
	if media.Status != domain.MediaStatusPending || media.MessageID == nil || *media.MessageID != db.messages[0].ID {
		t.Fatalf("media = %+v", media)
	}
	if media.StoredAt != nil || media.StorageKey != nil {
		t.Fatalf("media must not be downloaded: %+v", media)
	}
	resource := decodeMessageEvent(t, db.outbox[0])
	if resource.Media == nil || resource.Media.Status != domain.MediaStatusPending || resource.Media.DownloadURL != nil {
		t.Fatalf("resource media = %+v", resource.Media)
	}
}

func TestIngestMessageWithDownloadDisabledMarksMediaSkipped(t *testing.T) {
	db := newFakeDB()
	db.settings.DownloadMedia = false
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, imageMessage("WM2")); err != nil {
		t.Fatal(err)
	}
	if len(db.media) != 1 || db.media[0].Status != domain.MediaStatusSkipped {
		t.Fatalf("media = %+v", db.media)
	}
}

func TestIngestMessageResolvesLIDChatToCanonicalPN(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	lid := "98765432109876@lid"
	byLID := textMessage("WL1", false)
	byLID.Chat = ChatRef{JID: lid, LID: &lid}
	if err := service.IngestMessage(context.Background(), testInstance, byLID); err != nil {
		t.Fatal(err)
	}
	byPN := textMessage("WL2", false)
	byPN.Chat = ChatRef{JID: "5511988887777@s.whatsapp.net", Phone: pointer("5511988887777"), LID: &lid}
	if err := service.IngestMessage(context.Background(), testInstance, byPN); err != nil {
		t.Fatal(err)
	}
	if len(db.chats) != 1 {
		t.Fatalf("chats = %d, want 1", len(db.chats))
	}
	chat := db.chats[0]
	if chat.JID != "5511988887777@s.whatsapp.net" || chat.LID == nil || *chat.LID != lid || chat.Phone == nil {
		t.Fatalf("chat = %+v", chat)
	}
	if db.messages[0].ChatID != db.messages[1].ChatID {
		t.Fatal("messages must share the canonical chat")
	}
}

func TestIngestMessageLinksLIDToExistingPNChat(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WP1", false)); err != nil {
		t.Fatal(err)
	}
	lid := "98765432109876@lid"
	next := textMessage("WP2", false)
	next.Chat.LID = &lid
	if err := service.IngestMessage(context.Background(), testInstance, next); err != nil {
		t.Fatal(err)
	}
	if len(db.chats) != 1 || db.chats[0].LID == nil || *db.chats[0].LID != lid {
		t.Fatalf("chats = %+v", db.chats)
	}
}

func TestUndecryptableIsReplacedWhenContentArrives(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	placeholder := textMessage("WU1", false)
	placeholder.Content = Content{Type: MessageTypeUndecryptable}
	if err := service.IngestMessage(context.Background(), testInstance, placeholder); err != nil {
		t.Fatal(err)
	}
	if err := service.IngestMessage(context.Background(), testInstance, placeholder); err != nil {
		t.Fatal(err)
	}
	if len(db.outbox) != 1 {
		t.Fatalf("placeholder events = %d", len(db.outbox))
	}
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WU1", false)); err != nil {
		t.Fatal(err)
	}
	if len(db.messages) != 1 || db.messages[0].Type != MessageTypeText || len(db.outbox) != 2 {
		t.Fatalf("messages = %+v outbox = %d", db.messages, len(db.outbox))
	}
	if resource := decodeMessageEvent(t, db.outbox[1]); resource.Type != MessageTypeText || resource.Text == nil {
		t.Fatalf("resource = %+v", resource)
	}
}

func TestEditRevocationAndReactionEmitUpdated(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WE1", false)); err != nil {
		t.Fatal(err)
	}
	db.outbox = nil
	ctx := context.Background()
	if err := service.IngestEdit(ctx, testInstance, EditChange{
		Chat: directChat(), TargetWAMessageID: "WE1", Content: Content{Text: pointer("new text")}, At: baseTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.IngestReaction(ctx, testInstance, ReactionChange{
		Chat: directChat(), TargetWAMessageID: "WE1", SenderJID: "5511999999999@s.whatsapp.net", Emoji: "+", At: baseTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.IngestRevocation(ctx, testInstance, RevocationChange{Chat: directChat(), TargetWAMessageID: "WE1", At: baseTime}); err != nil {
		t.Fatal(err)
	}
	if err := service.IngestRevocation(ctx, testInstance, RevocationChange{Chat: directChat(), TargetWAMessageID: "WE1", At: baseTime}); err != nil {
		t.Fatal(err)
	}
	changes := []string{}
	for _, event := range db.outbox {
		var data MessageUpdatedData
		if err := json.Unmarshal(event.Payload, &data); err != nil {
			t.Fatal(err)
		}
		if event.EventType != EventTypeMessageUpdated {
			t.Fatalf("event type = %s", event.EventType)
		}
		changes = append(changes, data.Change)
	}
	want := []string{ChangeEdited, ChangeReaction, ChangeRevoked}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	for index := range want {
		if changes[index] != want[index] {
			t.Fatalf("changes = %v, want %v", changes, want)
		}
	}
}

func TestChangeForUnknownTargetIsDropped(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	ctx := context.Background()
	if err := service.IngestEdit(ctx, testInstance, EditChange{Chat: directChat(), TargetWAMessageID: "missing", At: baseTime}); err != nil {
		t.Fatal(err)
	}
	if err := service.IngestReaction(ctx, testInstance, ReactionChange{Chat: directChat(), TargetWAMessageID: "missing", Emoji: "+", At: baseTime}); err != nil {
		t.Fatal(err)
	}
	if len(db.outbox) != 0 {
		t.Fatalf("outbox = %d", len(db.outbox))
	}
}

func TestEventIDsAreUnique(t *testing.T) {
	db := newFakeDB()
	service := newTestService(db)
	for _, id := range []string{"U1", "U2", "U3"} {
		if err := service.IngestMessage(context.Background(), testInstance, textMessage(id, false)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uuid.UUID]bool{}
	for _, event := range db.outbox {
		if seen[event.EventID] {
			t.Fatalf("duplicated event id %s", event.EventID)
		}
		seen[event.EventID] = true
	}
}
