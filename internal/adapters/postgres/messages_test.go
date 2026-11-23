package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

type messageFixture struct {
	repository *postgres.MessageRepository
	instance   *domain.Instance
	chat       *domain.Chat
}

func newMessageFixture(t *testing.T) messageFixture {
	t.Helper()
	pool := newTestPool(t)
	instance := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
	return messageFixture{repository: postgres.NewMessageRepository(pool), instance: instance, chat: chat}
}

func (f messageFixture) insert(t *testing.T, status string, timestamp time.Time) *domain.Message {
	t.Helper()
	message := newMessage(f.instance.ID, f.chat.ID, status, timestamp)
	inserted, err := f.repository.Insert(context.Background(), message)
	requireNoError(t, err)
	if !inserted {
		t.Fatalf("message %s was not inserted", message.WAMessageID)
	}
	return message
}

func TestMessageInsertIsIdempotentPerWAMessageID(t *testing.T) {
	fixture := newMessageFixture(t)
	original := fixture.insert(t, domain.MessageStatusSent, microsecondsNow())
	duplicate := newMessage(fixture.instance.ID, fixture.chat.ID, domain.MessageStatusReceived, microsecondsNow())
	duplicate.WAMessageID = original.WAMessageID

	inserted, err := fixture.repository.Insert(context.Background(), duplicate)

	requireNoError(t, err)
	if inserted {
		t.Fatal("expected duplicate insert to report inserted=false")
	}
	found, err := fixture.repository.FindByChatAndWAMessageID(context.Background(), fixture.instance.ID, fixture.chat.ID, original.WAMessageID)
	requireNoError(t, err)
	if found.ID != original.ID || found.Status != domain.MessageStatusSent {
		t.Fatalf("original row was altered: %+v", found)
	}
}

func TestMessageInsertSameWAMessageIDInDifferentChatIsAllowed(t *testing.T) {
	pool := newTestPool(t)
	instance := newInstance(t, pool, uniqueClientID())
	firstChat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
	secondChat := newChat(t, pool, instance.ID, uuid.NewString()+"@s.whatsapp.net")
	repository := postgres.NewMessageRepository(pool)
	first := newMessage(instance.ID, firstChat.ID, domain.MessageStatusSent, microsecondsNow())
	_, err := repository.Insert(context.Background(), first)
	requireNoError(t, err)
	second := newMessage(instance.ID, secondChat.ID, domain.MessageStatusSent, microsecondsNow())
	second.WAMessageID = first.WAMessageID

	inserted, err := repository.Insert(context.Background(), second)

	requireNoError(t, err)
	if !inserted {
		t.Fatal("expected insert in a different chat to succeed")
	}
}

func TestMessageInsertReusedIdempotencyKeyConflicts(t *testing.T) {
	fixture := newMessageFixture(t)
	key := "key-" + uuid.NewString()
	first := newMessage(fixture.instance.ID, fixture.chat.ID, domain.MessageStatusPending, microsecondsNow())
	first.IdempotencyKey = &key
	_, err := fixture.repository.Insert(context.Background(), first)
	requireNoError(t, err)
	second := newMessage(fixture.instance.ID, fixture.chat.ID, domain.MessageStatusPending, microsecondsNow())
	second.IdempotencyKey = &key

	_, err = fixture.repository.Insert(context.Background(), second)

	if !errors.Is(err, postgres.ErrIdempotencyKeyTaken) {
		t.Fatalf("expected ErrIdempotencyKeyTaken, got %v", err)
	}
}

func TestMessageFindByIDScopedToInstance(t *testing.T) {
	fixture := newMessageFixture(t)
	message := fixture.insert(t, domain.MessageStatusSent, microsecondsNow())

	found, err := fixture.repository.FindByID(context.Background(), fixture.instance.ID, message.ID)
	requireNoError(t, err)
	if found.ID != message.ID {
		t.Fatalf("expected %s, got %s", message.ID, found.ID)
	}

	_, err = fixture.repository.FindByID(context.Background(), uuid.New(), message.ID)
	requireAppError(t, err, domain.MessageNotFound())
}

func TestMessageAdvanceStatus(t *testing.T) {
	tests := []struct {
		name           string
		current        string
		next           string
		expectAdvanced bool
		expectedStatus string
	}{
		{name: "pending to sent", current: domain.MessageStatusPending, next: domain.MessageStatusSent, expectAdvanced: true, expectedStatus: domain.MessageStatusSent},
		{name: "sent to delivered", current: domain.MessageStatusSent, next: domain.MessageStatusDelivered, expectAdvanced: true, expectedStatus: domain.MessageStatusDelivered},
		{name: "delivered to read", current: domain.MessageStatusDelivered, next: domain.MessageStatusRead, expectAdvanced: true, expectedStatus: domain.MessageStatusRead},
		{name: "read to played", current: domain.MessageStatusRead, next: domain.MessageStatusPlayed, expectAdvanced: true, expectedStatus: domain.MessageStatusPlayed},
		{name: "sent skips to read", current: domain.MessageStatusSent, next: domain.MessageStatusRead, expectAdvanced: true, expectedStatus: domain.MessageStatusRead},
		{name: "delivered does not regress to sent", current: domain.MessageStatusDelivered, next: domain.MessageStatusSent, expectAdvanced: false, expectedStatus: domain.MessageStatusDelivered},
		{name: "read does not regress to delivered", current: domain.MessageStatusRead, next: domain.MessageStatusDelivered, expectAdvanced: false, expectedStatus: domain.MessageStatusRead},
		{name: "same status is not an advance", current: domain.MessageStatusSent, next: domain.MessageStatusSent, expectAdvanced: false, expectedStatus: domain.MessageStatusSent},
		{name: "failed never advances", current: domain.MessageStatusFailed, next: domain.MessageStatusRead, expectAdvanced: false, expectedStatus: domain.MessageStatusFailed},
		{name: "advancing to failed is ignored", current: domain.MessageStatusSent, next: domain.MessageStatusFailed, expectAdvanced: false, expectedStatus: domain.MessageStatusSent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMessageFixture(t)
			message := fixture.insert(t, test.current, microsecondsNow())

			advance, advanced, err := fixture.repository.AdvanceStatus(context.Background(), fixture.instance.ID, message.ID, test.next, microsecondsNow())

			requireNoError(t, err)
			if advanced != test.expectAdvanced {
				t.Fatalf("expected advanced=%v, got %v", test.expectAdvanced, advanced)
			}
			if advanced && (advance.PreviousStatus != test.current || advance.MessageID != message.ID || advance.ChatID != fixture.chat.ID) {
				t.Fatalf("unexpected advance %+v", advance)
			}
			stored, err := fixture.repository.FindByID(context.Background(), fixture.instance.ID, message.ID)
			requireNoError(t, err)
			if stored.Status != test.expectedStatus {
				t.Fatalf("expected stored status %s, got %s", test.expectedStatus, stored.Status)
			}
		})
	}
}

func TestMessageAdvanceStatusSetsStatusTimestampOnce(t *testing.T) {
	fixture := newMessageFixture(t)
	message := fixture.insert(t, domain.MessageStatusSent, microsecondsNow())
	deliveredAt := microsecondsNow().Add(-time.Minute)

	_, advanced, err := fixture.repository.AdvanceStatus(context.Background(), fixture.instance.ID, message.ID, domain.MessageStatusDelivered, deliveredAt)

	requireNoError(t, err)
	if !advanced {
		t.Fatal("expected advance")
	}
	stored, err := fixture.repository.FindByID(context.Background(), fixture.instance.ID, message.ID)
	requireNoError(t, err)
	if stored.DeliveredAt == nil || !stored.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("expected delivered_at %v, got %v", deliveredAt, stored.DeliveredAt)
	}
}

func TestMessageAdvanceStatusUnknownMessage(t *testing.T) {
	fixture := newMessageFixture(t)

	_, advanced, err := fixture.repository.AdvanceStatus(context.Background(), fixture.instance.ID, uuid.New(), domain.MessageStatusRead, microsecondsNow())

	requireNoError(t, err)
	if advanced {
		t.Fatal("expected no advance for unknown message")
	}
}

func TestMessageAdvanceStatusByWAMessageIDReturnsPreviousStatus(t *testing.T) {
	fixture := newMessageFixture(t)
	message := fixture.insert(t, domain.MessageStatusSent, microsecondsNow())

	advances, err := fixture.repository.AdvanceStatusByWAMessageID(context.Background(), fixture.instance.ID, message.WAMessageID, domain.MessageStatusDelivered, microsecondsNow())

	requireNoError(t, err)
	if len(advances) != 1 {
		t.Fatalf("expected 1 advance, got %d", len(advances))
	}
	if advances[0].PreviousStatus != domain.MessageStatusSent || advances[0].MessageID != message.ID {
		t.Fatalf("unexpected advance %+v", advances[0])
	}
}

func TestMessageAdvanceStatusByWAMessageIDDoesNotRegress(t *testing.T) {
	fixture := newMessageFixture(t)
	message := fixture.insert(t, domain.MessageStatusRead, microsecondsNow())

	advances, err := fixture.repository.AdvanceStatusByWAMessageID(context.Background(), fixture.instance.ID, message.WAMessageID, domain.MessageStatusDelivered, microsecondsNow())

	requireNoError(t, err)
	if len(advances) != 0 {
		t.Fatalf("expected no advances, got %+v", advances)
	}
}

func TestMessageMarkFailedOnlyFromPending(t *testing.T) {
	tests := []struct {
		name           string
		current        string
		expectMarked   bool
		expectedStatus string
	}{
		{name: "pending becomes failed", current: domain.MessageStatusPending, expectMarked: true, expectedStatus: domain.MessageStatusFailed},
		{name: "sent stays sent", current: domain.MessageStatusSent, expectMarked: false, expectedStatus: domain.MessageStatusSent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMessageFixture(t)
			message := fixture.insert(t, test.current, microsecondsNow())

			marked, err := fixture.repository.MarkFailed(context.Background(), fixture.instance.ID, message.ID, "SEND_ERROR", "boom")

			requireNoError(t, err)
			if marked != test.expectMarked {
				t.Fatalf("expected marked=%v, got %v", test.expectMarked, marked)
			}
			stored, err := fixture.repository.FindByID(context.Background(), fixture.instance.ID, message.ID)
			requireNoError(t, err)
			if stored.Status != test.expectedStatus {
				t.Fatalf("expected status %s, got %s", test.expectedStatus, stored.Status)
			}
		})
	}
}

func TestMessageListPaginatesWithoutRepeatingRows(t *testing.T) {
	fixture := newMessageFixture(t)
	base := microsecondsNow().Add(-time.Hour)
	expectedOrder := make([]uuid.UUID, 0, 5)
	for offset := range 5 {
		message := fixture.insert(t, domain.MessageStatusSent, base.Add(time.Duration(offset)*time.Minute))
		expectedOrder = append([]uuid.UUID{message.ID}, expectedOrder...)
	}
	chatID := fixture.chat.ID
	filter := domain.MessageFilter{ChatID: &chatID}

	first, err := fixture.repository.List(context.Background(), fixture.instance.ID, filter, apihttp.PageParams{Limit: 2})
	requireNoError(t, err)
	if len(first.Data) != 2 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("unexpected first page: len=%d has_more=%v cursor=%v", len(first.Data), first.HasMore, first.NextCursor)
	}
	second, err := fixture.repository.List(context.Background(), fixture.instance.ID, filter, apihttp.PageParams{Limit: 2, Cursor: *first.NextCursor})
	requireNoError(t, err)
	if len(second.Data) != 2 || !second.HasMore || second.NextCursor == nil {
		t.Fatalf("unexpected second page: len=%d has_more=%v", len(second.Data), second.HasMore)
	}
	third, err := fixture.repository.List(context.Background(), fixture.instance.ID, filter, apihttp.PageParams{Limit: 2, Cursor: *second.NextCursor})
	requireNoError(t, err)
	if len(third.Data) != 1 || third.HasMore || third.NextCursor != nil {
		t.Fatalf("unexpected third page: len=%d has_more=%v", len(third.Data), third.HasMore)
	}

	collected := append(append(append([]domain.Message{}, first.Data...), second.Data...), third.Data...)
	for index, message := range collected {
		if message.ID != expectedOrder[index] {
			t.Fatalf("row %d: expected %s, got %s", index, expectedOrder[index], message.ID)
		}
	}
}

func TestMessageListFilters(t *testing.T) {
	fixture := newMessageFixture(t)
	now := microsecondsNow()
	inbound := newMessage(fixture.instance.ID, fixture.chat.ID, domain.MessageStatusReceived, now)
	inbound.Direction = domain.DirectionInbound
	inbound.Origin = "contact"
	inbound.Text = stringPointer("needle in text")
	_, err := fixture.repository.Insert(context.Background(), inbound)
	requireNoError(t, err)
	outbound := fixture.insert(t, domain.MessageStatusSent, now.Add(-time.Minute))

	tests := []struct {
		name        string
		filter      domain.MessageFilter
		expectedIDs []uuid.UUID
	}{
		{name: "direction inbound", filter: domain.MessageFilter{Direction: domain.DirectionInbound}, expectedIDs: []uuid.UUID{inbound.ID}},
		{name: "direction outbound", filter: domain.MessageFilter{Direction: domain.DirectionOutbound}, expectedIDs: []uuid.UUID{outbound.ID}},
		{name: "search text", filter: domain.MessageFilter{Search: "needle"}, expectedIDs: []uuid.UUID{inbound.ID}},
		{name: "wa message id", filter: domain.MessageFilter{WAMessageID: outbound.WAMessageID}, expectedIDs: []uuid.UUID{outbound.ID}},
		{name: "no filter", filter: domain.MessageFilter{}, expectedIDs: []uuid.UUID{inbound.ID, outbound.ID}},
		{name: "no match", filter: domain.MessageFilter{Search: "absent"}, expectedIDs: []uuid.UUID{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page, err := fixture.repository.List(context.Background(), fixture.instance.ID, test.filter, apihttp.PageParams{Limit: 10})

			requireNoError(t, err)
			if len(page.Data) != len(test.expectedIDs) {
				t.Fatalf("expected %d rows, got %d", len(test.expectedIDs), len(page.Data))
			}
			for index, message := range page.Data {
				if message.ID != test.expectedIDs[index] {
					t.Fatalf("row %d: expected %s, got %s", index, test.expectedIDs[index], message.ID)
				}
			}
		})
	}
}

func TestMessageListPendingForSendHoldsLaterMessagesBehindRetry(t *testing.T) {
	fixture := newMessageFixture(t)
	first := fixture.insert(t, domain.MessageStatusPending, microsecondsNow())
	second := fixture.insert(t, domain.MessageStatusPending, microsecondsNow())
	requireNoError(t, fixture.repository.ScheduleSendRetry(context.Background(), fixture.instance.ID, first.ID, time.Now().Add(time.Hour)))

	pending, err := fixture.repository.ListPendingForSend(context.Background(), fixture.instance.ID, 10)

	requireNoError(t, err)
	if len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatalf("expected only the retrying message, got %+v", pending)
	}
	requireNoError(t, fixture.repository.ScheduleSendRetry(context.Background(), fixture.instance.ID, first.ID, time.Now()))
	if _, err := fixture.repository.MarkFailed(context.Background(), fixture.instance.ID, first.ID, "X", "failed"); err != nil {
		t.Fatal(err)
	}
	pending, err = fixture.repository.ListPendingForSend(context.Background(), fixture.instance.ID, 10)
	requireNoError(t, err)
	if len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("expected second message after first resolved, got %+v", pending)
	}
}
