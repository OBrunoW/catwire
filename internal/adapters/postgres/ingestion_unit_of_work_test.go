package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

type ingestionFixture struct {
	pool     *pgxpool.Pool
	instance *domain.Instance
	service  *ingestion.Service
}

type fixedEventIDOutbox struct {
	inner   ingestion.OutboxWriter
	eventID uuid.UUID
}

func (o fixedEventIDOutbox) Insert(ctx context.Context, event *domain.OutboxEvent) error {
	event.EventID = o.eventID
	return o.inner.Insert(ctx, event)
}

type fixedEventIDUnitOfWork struct {
	inner   ingestion.UnitOfWork
	eventID uuid.UUID
}

func (u fixedEventIDUnitOfWork) Do(ctx context.Context, run func(unit ingestion.Unit) error) error {
	return u.inner.Do(ctx, func(unit ingestion.Unit) error {
		unit.Outbox = fixedEventIDOutbox{inner: unit.Outbox, eventID: u.eventID}
		return run(unit)
	})
}

func newIngestionFixture(t *testing.T, settings domain.InstanceSettings) *ingestionFixture {
	t.Helper()
	pool := newTestPool(t)
	clientID := uniqueClientID()
	registerOutboxCleanup(t, pool, clientID)
	instance := newInstance(t, pool, clientID)
	settings.InstanceID = instance.ID
	requireNoError(t, postgres.NewInstanceSettingsRepository(pool).Insert(context.Background(), &settings))
	service := ingestion.NewService(ingestion.Options{
		Unit:     postgres.NewIngestionUnitOfWork(pool),
		Settings: postgres.NewInstanceSettingsRepository(pool),
	})
	return &ingestionFixture{pool: pool, instance: instance, service: service}
}

func newIncomingMessage(chatJID string) ingestion.IncomingMessage {
	return ingestion.IncomingMessage{
		WAMessageID: "wa-" + uuid.NewString(),
		Chat:        ingestion.ChatRef{JID: chatJID},
		Sender:      ingestion.Sender{JID: chatJID},
		Timestamp:   microsecondsNow(),
		Source:      ingestion.SourceLive,
		Content:     ingestion.Content{Type: ingestion.MessageTypeText, Text: stringPointer("hello")},
	}
}

func newImageContent() ingestion.Content {
	return ingestion.Content{
		Type: ingestion.MessageTypeImage,
		Media: &ingestion.MediaDescriptor{
			Kind:       "image",
			MimeType:   stringPointer("image/jpeg"),
			DirectPath: stringPointer("/direct/path"),
			MediaKey:   []byte{1, 2, 3},
		},
	}
}

func (f *ingestionFixture) countMessages(t *testing.T) int {
	t.Helper()
	var count int
	err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM catwire.messages WHERE instance_id = $1`, f.instance.ID,
	).Scan(&count)
	requireNoError(t, err)
	return count
}

func (f *ingestionFixture) countChats(t *testing.T) int {
	t.Helper()
	var count int
	err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM catwire.chats WHERE instance_id = $1`, f.instance.ID,
	).Scan(&count)
	requireNoError(t, err)
	return count
}

func (f *ingestionFixture) countEventsOfType(t *testing.T, eventType string) int {
	t.Helper()
	var count int
	err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM catwire.outbox_events WHERE client_id = $1 AND event_type = $2`,
		f.instance.ClientID, eventType,
	).Scan(&count)
	requireNoError(t, err)
	return count
}

func (f *ingestionFixture) readMessage(t *testing.T, waMessageID string) *domain.Message {
	t.Helper()
	message, err := postgres.NewMessageRepository(f.pool).FindByWAMessageID(context.Background(), f.instance.ID, waMessageID)
	requireNoError(t, err)
	return message
}

type mediaRow struct {
	status    string
	messageID *uuid.UUID
}

func (f *ingestionFixture) readMedia(t *testing.T, messageID uuid.UUID) mediaRow {
	t.Helper()
	var row mediaRow
	err := f.pool.QueryRow(context.Background(),
		`SELECT status, message_id FROM catwire.media WHERE instance_id = $1 AND message_id = $2`,
		f.instance.ID, messageID,
	).Scan(&row.status, &row.messageID)
	requireNoError(t, err)
	return row
}

func downloadingSettings() domain.InstanceSettings {
	return domain.InstanceSettings{DownloadMedia: true}
}

func TestIngestionNewMessageWritesMessageChatAndOutboxTogether(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("5511999990001@s.whatsapp.net")

	err := fixture.service.IngestMessage(context.Background(), *fixture.instance, incoming)

	requireNoError(t, err)
	if count := fixture.countMessages(t); count != 1 {
		t.Fatalf("expected 1 message, got %d", count)
	}
	if count := fixture.countChats(t); count != 1 {
		t.Fatalf("expected 1 chat, got %d", count)
	}
	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageReceived); count != 1 {
		t.Fatalf("expected 1 received event, got %d", count)
	}
	stored := fixture.readMessage(t, incoming.WAMessageID)
	if stored.Direction != domain.DirectionInbound || stored.Origin != ingestion.OriginContact {
		t.Fatalf("unexpected direction %s origin %s", stored.Direction, stored.Origin)
	}
}

func TestIngestionRepeatedMessageDoesNotDuplicateMessageOrOutbox(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("5511999990002@s.whatsapp.net")
	ctx := context.Background()

	requireNoError(t, fixture.service.IngestMessage(ctx, *fixture.instance, incoming))
	requireNoError(t, fixture.service.IngestMessage(ctx, *fixture.instance, incoming))

	if count := fixture.countMessages(t); count != 1 {
		t.Fatalf("expected 1 message, got %d", count)
	}
	if count := countOutboxEvents(t, fixture.pool, fixture.instance.ClientID); count != 1 {
		t.Fatalf("expected 1 outbox event, got %d", count)
	}
}

func TestIngestionOwnDeviceMessageStoresDeviceOriginAndEmitsSent(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("5511999990003@s.whatsapp.net")
	incoming.FromMe = true

	requireNoError(t, fixture.service.IngestMessage(context.Background(), *fixture.instance, incoming))

	stored := fixture.readMessage(t, incoming.WAMessageID)
	if stored.Origin != ingestion.OriginDevice || stored.Direction != domain.DirectionOutbound {
		t.Fatalf("unexpected origin %s direction %s", stored.Origin, stored.Direction)
	}
	if stored.Status != domain.MessageStatusSent {
		t.Fatalf("expected status sent, got %s", stored.Status)
	}
	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageSent); count != 1 {
		t.Fatalf("expected 1 sent event, got %d", count)
	}
	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageReceived); count != 0 {
		t.Fatalf("expected no received event, got %d", count)
	}
}

func TestIngestionReceiptEmitsStatusUpdatedOnlyWhenStatusAdvances(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("5511999990004@s.whatsapp.net")
	incoming.FromMe = true
	ctx := context.Background()
	requireNoError(t, fixture.service.IngestMessage(ctx, *fixture.instance, incoming))
	receipt := ingestion.ReceiptChange{
		Chat:         incoming.Chat,
		WAMessageIDs: []string{incoming.WAMessageID},
		Status:       domain.MessageStatusDelivered,
		At:           microsecondsNow(),
	}

	requireNoError(t, fixture.service.IngestReceipt(ctx, *fixture.instance, receipt))
	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageStatusUpdated); count != 1 {
		t.Fatalf("expected 1 status event after advance, got %d", count)
	}
	requireNoError(t, fixture.service.IngestReceipt(ctx, *fixture.instance, receipt))

	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageStatusUpdated); count != 1 {
		t.Fatalf("expected still 1 status event, got %d", count)
	}
	if stored := fixture.readMessage(t, incoming.WAMessageID); stored.Status != domain.MessageStatusDelivered {
		t.Fatalf("expected status delivered, got %s", stored.Status)
	}
}

func TestIngestionReceiptDoesNotRegressStatus(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("5511999990005@s.whatsapp.net")
	incoming.FromMe = true
	ctx := context.Background()
	requireNoError(t, fixture.service.IngestMessage(ctx, *fixture.instance, incoming))
	readReceipt := ingestion.ReceiptChange{
		Chat: incoming.Chat, WAMessageIDs: []string{incoming.WAMessageID},
		Status: domain.MessageStatusRead, At: microsecondsNow(),
	}
	deliveredReceipt := readReceipt
	deliveredReceipt.Status = domain.MessageStatusDelivered

	requireNoError(t, fixture.service.IngestReceipt(ctx, *fixture.instance, readReceipt))
	requireNoError(t, fixture.service.IngestReceipt(ctx, *fixture.instance, deliveredReceipt))

	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageStatusUpdated); count != 1 {
		t.Fatalf("expected 1 status event, got %d", count)
	}
	if stored := fixture.readMessage(t, incoming.WAMessageID); stored.Status != domain.MessageStatusRead {
		t.Fatalf("expected status read, got %s", stored.Status)
	}
}

func TestIngestionIgnoredGroupWritesNothing(t *testing.T) {
	settings := downloadingSettings()
	settings.IgnoreGroups = true
	fixture := newIngestionFixture(t, settings)
	incoming := newIncomingMessage("120363000000000001@g.us")
	incoming.Chat.Group = true

	requireNoError(t, fixture.service.IngestMessage(context.Background(), *fixture.instance, incoming))

	if count := fixture.countMessages(t); count != 0 {
		t.Fatalf("expected no messages, got %d", count)
	}
	if count := fixture.countChats(t); count != 0 {
		t.Fatalf("expected no chats, got %d", count)
	}
	if count := countOutboxEvents(t, fixture.pool, fixture.instance.ClientID); count != 0 {
		t.Fatalf("expected no outbox events, got %d", count)
	}
}

func TestIngestionGroupIsStoredWhenGroupsAreNotIgnored(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("120363000000000002@g.us")
	incoming.Chat.Group = true

	requireNoError(t, fixture.service.IngestMessage(context.Background(), *fixture.instance, incoming))

	if count := fixture.countMessages(t); count != 1 {
		t.Fatalf("expected 1 message, got %d", count)
	}
	if count := fixture.countEventsOfType(t, ingestion.EventTypeMessageReceived); count != 1 {
		t.Fatalf("expected 1 received event, got %d", count)
	}
}

func TestIngestionHistoryBatchStoresMessagesAndEmitsSingleSummaryEvent(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	chat := ingestion.ChatRef{JID: "5511999990006@s.whatsapp.net"}
	otherChat := ingestion.ChatRef{JID: "5511999990007@s.whatsapp.net"}
	batch := ingestion.HistoryBatch{
		SyncType: "initial",
		Conversations: []ingestion.HistoryConversation{
			{Chat: chat, Messages: []ingestion.IncomingMessage{newIncomingMessage(chat.JID), newIncomingMessage(chat.JID)}},
			{Chat: otherChat, Messages: []ingestion.IncomingMessage{newIncomingMessage(otherChat.JID)}},
		},
	}

	requireNoError(t, fixture.service.IngestHistory(context.Background(), *fixture.instance, batch))

	if count := fixture.countMessages(t); count != 3 {
		t.Fatalf("expected 3 messages, got %d", count)
	}
	var historyMessages int
	err := fixture.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM catwire.messages WHERE instance_id = $1 AND source = 'history'`, fixture.instance.ID,
	).Scan(&historyMessages)
	requireNoError(t, err)
	if historyMessages != 3 {
		t.Fatalf("expected 3 history messages, got %d", historyMessages)
	}
	if count := fixture.countEventsOfType(t, ingestion.EventTypeHistorySynced); count != 1 {
		t.Fatalf("expected 1 history event, got %d", count)
	}
	if count := countOutboxEvents(t, fixture.pool, fixture.instance.ClientID); count != 1 {
		t.Fatalf("expected exactly 1 outbox event, got %d", count)
	}
}

func TestIngestionFailedOutboxInsertRollsBackMessage(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	ctx := context.Background()
	existingEventID := uuid.New()
	instanceID := fixture.instance.ID
	requireNoError(t, postgres.NewOutboxRepository(fixture.pool).Insert(ctx, &domain.OutboxEvent{
		EventID:      existingEventID,
		EventType:    ingestion.EventTypeMessageReceived,
		EventVersion: 1,
		ClientID:     fixture.instance.ClientID,
		InstanceID:   &instanceID,
		PartitionKey: "seed",
		Payload:      []byte(`{}`),
		OccurredAt:   microsecondsNow(),
	}))
	failing := ingestion.NewService(ingestion.Options{
		Unit:     fixedEventIDUnitOfWork{inner: postgres.NewIngestionUnitOfWork(fixture.pool), eventID: existingEventID},
		Settings: postgres.NewInstanceSettingsRepository(fixture.pool),
	})
	incoming := newIncomingMessage("5511999990008@s.whatsapp.net")
	incoming.Content = newImageContent()

	err := failing.IngestMessage(ctx, *fixture.instance, incoming)

	if err == nil {
		t.Fatal("expected error from duplicate event id")
	}
	if count := fixture.countMessages(t); count != 0 {
		t.Fatalf("expected message rolled back, got %d", count)
	}
	if count := fixture.countChats(t); count != 0 {
		t.Fatalf("expected chat rolled back, got %d", count)
	}
	var mediaCount int
	scanErr := fixture.pool.QueryRow(ctx,
		`SELECT count(*) FROM catwire.media WHERE instance_id = $1`, fixture.instance.ID,
	).Scan(&mediaCount)
	requireNoError(t, scanErr)
	if mediaCount != 0 {
		t.Fatalf("expected media rolled back, got %d", mediaCount)
	}
	if count := countOutboxEvents(t, fixture.pool, fixture.instance.ClientID); count != 1 {
		t.Fatalf("expected only the seeded outbox event, got %d", count)
	}
}

func TestIngestionLiveMediaIsStoredAsPendingLinkedToMessage(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	incoming := newIncomingMessage("5511999990009@s.whatsapp.net")
	incoming.Content = newImageContent()

	requireNoError(t, fixture.service.IngestMessage(context.Background(), *fixture.instance, incoming))

	stored := fixture.readMessage(t, incoming.WAMessageID)
	media := fixture.readMedia(t, stored.ID)
	if media.status != domain.MediaStatusPending {
		t.Fatalf("expected media pending, got %s", media.status)
	}
	if media.messageID == nil || *media.messageID != stored.ID {
		t.Fatalf("expected media linked to message %s", stored.ID)
	}
}

func TestIngestionLiveMediaIsSkippedWhenDownloadIsDisabled(t *testing.T) {
	fixture := newIngestionFixture(t, domain.InstanceSettings{DownloadMedia: false})
	incoming := newIncomingMessage("5511999990010@s.whatsapp.net")
	incoming.Content = newImageContent()

	requireNoError(t, fixture.service.IngestMessage(context.Background(), *fixture.instance, incoming))

	stored := fixture.readMessage(t, incoming.WAMessageID)
	if media := fixture.readMedia(t, stored.ID); media.status != domain.MediaStatusSkipped {
		t.Fatalf("expected media skipped, got %s", media.status)
	}
}

func TestIngestionHistoryMediaIsSkipped(t *testing.T) {
	fixture := newIngestionFixture(t, downloadingSettings())
	chat := ingestion.ChatRef{JID: "5511999990011@s.whatsapp.net"}
	incoming := newIncomingMessage(chat.JID)
	incoming.Content = newImageContent()
	batch := ingestion.HistoryBatch{
		SyncType:      "initial",
		Conversations: []ingestion.HistoryConversation{{Chat: chat, Messages: []ingestion.IncomingMessage{incoming}}},
	}

	requireNoError(t, fixture.service.IngestHistory(context.Background(), *fixture.instance, batch))

	stored := fixture.readMessage(t, incoming.WAMessageID)
	if media := fixture.readMedia(t, stored.ID); media.status != domain.MediaStatusSkipped {
		t.Fatalf("expected media skipped, got %s", media.status)
	}
}
