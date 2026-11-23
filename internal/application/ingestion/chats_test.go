package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type spyChats struct {
	*fakeChats
	updatedJIDs []string
}

func (s *spyChats) UpdateJID(ctx context.Context, instanceID, id uuid.UUID, jid string, phone *string) error {
	s.updatedJIDs = append(s.updatedJIDs, jid)
	return s.fakeChats.UpdateJID(ctx, instanceID, id, jid, phone)
}

func TestLIDOnlyMessageKeepsPNCanonicalJIDWhenPNIsUnresolved(t *testing.T) {
	db := newFakeDB()
	lid := "98765432109876@lid"
	pnJID := "5511988887777@s.whatsapp.net"
	db.chats = []*domain.Chat{{ID: uuid.New(), JID: pnJID, LID: &lid, Phone: pointer("5511988887777")}}
	chats := &spyChats{fakeChats: &fakeChats{db: db}}

	chat, err := resolveChat(context.Background(), chats, testInstance.ID, ChatRef{JID: lid, LID: &lid})
	if err != nil {
		t.Fatal(err)
	}
	if len(chats.updatedJIDs) != 0 {
		t.Fatalf("UpdateJID called with %v", chats.updatedJIDs)
	}
	if chat.JID != pnJID || db.chats[0].JID != pnJID {
		t.Fatalf("chat = %+v stored = %+v", chat, db.chats[0])
	}
}

type conflictingUnitOfWork struct {
	inner     UnitOfWork
	conflicts int
	runs      int
}

func (u *conflictingUnitOfWork) Do(ctx context.Context, run func(unit Unit) error) error {
	u.runs++
	if u.runs <= u.conflicts {
		return domain.ErrChatIdentityConflict
	}
	return u.inner.Do(ctx, run)
}

func TestIngestMessageRunsUnitAgainAfterIdentityConflict(t *testing.T) {
	db := newFakeDB()
	unit := &conflictingUnitOfWork{inner: db, conflicts: 1}
	service := NewService(Options{Unit: unit, Settings: db, Now: func() time.Time { return baseTime }})

	if err := service.IngestMessage(context.Background(), testInstance, textMessage("WA1", false)); err != nil {
		t.Fatal(err)
	}
	if unit.runs != 2 || len(db.chats) != 1 || len(db.messages) != 1 {
		t.Fatalf("runs = %d chats = %d messages = %d", unit.runs, len(db.chats), len(db.messages))
	}
}

func TestIngestMessageReturnsWrappedErrorAfterTwoIdentityConflicts(t *testing.T) {
	db := newFakeDB()
	unit := &conflictingUnitOfWork{inner: db, conflicts: 5}
	service := NewService(Options{Unit: unit, Settings: db, Now: func() time.Time { return baseTime }})

	err := service.IngestMessage(context.Background(), testInstance, textMessage("WA1", false))
	if !errors.Is(err, domain.ErrChatIdentityConflict) {
		t.Fatalf("err = %v", err)
	}
	if unit.runs != 2 {
		t.Fatalf("runs = %d, want 2", unit.runs)
	}
}
