package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func TestListInboundReceivedReturnsOnlyUnreadInbound(t *testing.T) {
	fixture := newMessageFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	outbound := fixture.insert(t, domain.MessageStatusSent, base)
	inbound := newMessage(fixture.instance.ID, fixture.chat.ID, domain.MessageStatusReceived, base.Add(time.Second))
	inbound.Direction = domain.DirectionInbound
	inbound.Origin = "contact"
	inserted, err := fixture.repository.Insert(context.Background(), inbound)
	requireNoError(t, err)
	if !inserted {
		t.Fatal("inbound message was not inserted")
	}

	found, err := fixture.repository.ListInboundReceived(context.Background(), fixture.instance.ID, fixture.chat.ID)

	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != inbound.ID || found[0].ID == outbound.ID {
		t.Fatalf("unexpected messages: %+v", found)
	}
}

func TestFindOldestInChatReturnsEarliestMessage(t *testing.T) {
	fixture := newMessageFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	oldest := fixture.insert(t, domain.MessageStatusSent, base)
	fixture.insert(t, domain.MessageStatusSent, base.Add(time.Minute))

	found, err := fixture.repository.FindOldestInChat(context.Background(), fixture.instance.ID, fixture.chat.ID)

	requireNoError(t, err)
	if found.ID != oldest.ID {
		t.Fatalf("expected oldest message %s, got %s", oldest.ID, found.ID)
	}
}

func TestFindOldestInChatWithoutMessagesIsNotFound(t *testing.T) {
	fixture := newMessageFixture(t)

	_, err := fixture.repository.FindOldestInChat(context.Background(), fixture.instance.ID, uuid.New())

	requireAppError(t, err, domain.MessageNotFound())
}

func TestFindByChatAndIDsIgnoresOtherChats(t *testing.T) {
	fixture := newMessageFixture(t)
	inChat := fixture.insert(t, domain.MessageStatusSent, time.Now().UTC().Truncate(time.Microsecond))

	found, err := fixture.repository.FindByChatAndIDs(
		context.Background(), fixture.instance.ID, fixture.chat.ID, []uuid.UUID{inChat.ID, uuid.New()},
	)

	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != inChat.ID {
		t.Fatalf("unexpected messages: %+v", found)
	}
}
