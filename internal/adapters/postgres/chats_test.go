package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

func TestChatUpsertCreatesAndIsIdempotentPerJID(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	jid := "5511999990000@s.whatsapp.net"
	first := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: jid, Type: domain.ChatTypeDirect}
	requireNoError(t, repository.Upsert(context.Background(), first))
	second := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: jid, Type: domain.ChatTypeDirect, Name: stringPointer("Alice")}

	err := repository.Upsert(context.Background(), second)

	requireNoError(t, err)
	if second.ID != first.ID {
		t.Fatalf("expected existing chat id %s, got %s", first.ID, second.ID)
	}
	if second.Name == nil || *second.Name != "Alice" {
		t.Fatalf("expected name to be filled, got %v", second.Name)
	}
}

func TestChatUpsertKeepsExistingNameWhenNewIsNil(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	jid := "5511999990001@s.whatsapp.net"
	named := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: jid, Type: domain.ChatTypeDirect, Name: stringPointer("Bob")}
	requireNoError(t, repository.Upsert(context.Background(), named))
	unnamed := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: jid, Type: domain.ChatTypeDirect}

	requireNoError(t, repository.Upsert(context.Background(), unnamed))

	if unnamed.Name == nil || *unnamed.Name != "Bob" {
		t.Fatalf("expected name Bob kept, got %v", unnamed.Name)
	}
}

func TestChatFindByJIDAndLID(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	jid := "5511999990002@s.whatsapp.net"
	lid := uuid.NewString() + "@lid"
	chat := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: jid, LID: &lid, Type: domain.ChatTypeDirect}
	requireNoError(t, repository.Upsert(context.Background(), chat))

	tests := []struct {
		name string
		find func() (*domain.Chat, error)
	}{
		{name: "by jid", find: func() (*domain.Chat, error) { return repository.FindByJID(context.Background(), instance.ID, jid) }},
		{name: "by lid", find: func() (*domain.Chat, error) { return repository.FindByLID(context.Background(), instance.ID, lid) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := test.find()

			requireNoError(t, err)
			if found.ID != chat.ID {
				t.Fatalf("expected chat %s, got %s", chat.ID, found.ID)
			}
		})
	}
}

func TestChatFindNotFound(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	otherInstance := newInstance(t, pool, uniqueClientID())
	jid := "5511999990003@s.whatsapp.net"
	newChat(t, pool, otherInstance.ID, jid)

	_, err := repository.FindByJID(context.Background(), instance.ID, jid)
	requireAppError(t, err, domain.ChatNotFound())

	_, err = repository.FindByLID(context.Background(), instance.ID, "missing@lid")
	requireAppError(t, err, domain.ChatNotFound())
}

func TestChatUpdateJIDPromotesLIDToPhoneNumber(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	lid := uuid.NewString() + "@lid"
	chat := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: lid, LID: &lid, Type: domain.ChatTypeDirect}
	requireNoError(t, repository.Upsert(context.Background(), chat))
	phoneJID := "5511999990004@s.whatsapp.net"

	err := repository.UpdateJID(context.Background(), instance.ID, chat.ID, phoneJID, stringPointer("5511999990004"))

	requireNoError(t, err)
	promoted, err := repository.FindByJID(context.Background(), instance.ID, phoneJID)
	requireNoError(t, err)
	if promoted.ID != chat.ID || promoted.Phone == nil || *promoted.Phone != "5511999990004" {
		t.Fatalf("unexpected promoted chat %+v", promoted)
	}
	if promoted.LID == nil || *promoted.LID != lid {
		t.Fatalf("expected lid kept, got %v", promoted.LID)
	}
	_, err = repository.FindByJID(context.Background(), instance.ID, lid)
	requireAppError(t, err, domain.ChatNotFound())
	byLID, err := repository.FindByLID(context.Background(), instance.ID, lid)
	requireNoError(t, err)
	if byLID.ID != chat.ID {
		t.Fatalf("expected lookup by lid to keep resolving chat %s, got %s", chat.ID, byLID.ID)
	}
}

func TestChatUpdateJIDUnknownChat(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)

	err := repository.UpdateJID(context.Background(), uuid.New(), uuid.New(), "x@s.whatsapp.net", nil)

	requireAppError(t, err, domain.ChatNotFound())
}

func TestChatTouchLastMessageNeverMovesBackwards(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, instance.ID, "5511999990005@s.whatsapp.net")
	newer := insertMessage(t, pool, newMessage(instance.ID, chat.ID, domain.MessageStatusSent, microsecondsNow()))
	older := insertMessage(t, pool, newMessage(instance.ID, chat.ID, domain.MessageStatusSent, microsecondsNow().Add(-time.Hour)))
	requireNoError(t, repository.TouchLastMessage(context.Background(), instance.ID, chat.ID, newer.ID, newer.Timestamp))

	requireNoError(t, repository.TouchLastMessage(context.Background(), instance.ID, chat.ID, older.ID, older.Timestamp))

	view, err := repository.FindByID(context.Background(), instance.ID, chat.ID)
	requireNoError(t, err)
	if view.LastMessage == nil || view.LastMessage.ID != newer.ID {
		t.Fatalf("expected last message %s, got %+v", newer.ID, view.LastMessage)
	}
}

func TestChatUpsertAndUpdateJIDReportIdentityConflict(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	first := newChat(t, pool, instance.ID, "5511999990006@s.whatsapp.net")
	other := newChat(t, pool, instance.ID, "5511999990007@s.whatsapp.net")

	err := repository.UpdateJID(context.Background(), instance.ID, other.ID, first.JID, nil)
	if !errors.Is(err, postgres.ErrChatIdentityConflict) {
		t.Fatalf("expected ErrChatIdentityConflict on jid, got %v", err)
	}

	lid := uuid.NewString() + "@lid"
	withLID := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: "5511999990008@s.whatsapp.net", LID: &lid, Type: domain.ChatTypeDirect}
	requireNoError(t, repository.Upsert(context.Background(), withLID))
	duplicate := &domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: "5511999990009@s.whatsapp.net", LID: &lid, Type: domain.ChatTypeDirect}
	err = repository.Upsert(context.Background(), duplicate)
	if !errors.Is(err, postgres.ErrChatIdentityConflict) {
		t.Fatalf("expected ErrChatIdentityConflict on lid, got %v", err)
	}
}

func TestChatTouchLastMessageIgnoresOtherInstance(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, instance.ID, "5511999990010@s.whatsapp.net")
	message := insertMessage(t, pool, newMessage(instance.ID, chat.ID, domain.MessageStatusSent, microsecondsNow()))

	requireNoError(t, repository.TouchLastMessage(context.Background(), uuid.New(), chat.ID, message.ID, message.Timestamp))

	view, err := repository.FindByID(context.Background(), instance.ID, chat.ID)
	requireNoError(t, err)
	if view.LastMessage != nil {
		t.Fatalf("expected no last message, got %+v", view.LastMessage)
	}
}
