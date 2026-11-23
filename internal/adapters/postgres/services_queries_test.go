package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/application/instance"
	"github.com/OBrunoW/catwire/internal/domain"
)

func newInstanceWithSettings(t *testing.T, pool *pgxpool.Pool, ignoreGroups bool) *domain.Instance {
	t.Helper()
	created := newInstance(t, pool, uniqueClientID())
	settings := &domain.InstanceSettings{InstanceID: created.ID, SyncHistory: true, IgnoreGroups: ignoreGroups}
	requireNoError(t, postgres.NewInstanceSettingsRepository(pool).Insert(context.Background(), settings))
	return created
}

func TestSettingsFindByInstanceIDsReturnsRequestedInstances(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceSettingsRepository(pool)
	first := newInstanceWithSettings(t, pool, true)
	second := newInstanceWithSettings(t, pool, false)
	notRequested := newInstanceWithSettings(t, pool, false)

	found, err := repository.FindByInstanceIDs(context.Background(), []uuid.UUID{first.ID, second.ID})

	requireNoError(t, err)
	if len(found) != 2 {
		t.Fatalf("expected 2 settings, got %d", len(found))
	}
	if !found[first.ID].IgnoreGroups || found[second.ID].IgnoreGroups {
		t.Fatalf("unexpected settings %+v", found)
	}
	if _, present := found[notRequested.ID]; present {
		t.Fatalf("settings of a non requested instance were returned")
	}
}

func TestSettingsFindByInstanceIDsIgnoresUnknownIDs(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceSettingsRepository(pool)
	existing := newInstanceWithSettings(t, pool, false)
	withoutSettings := newInstance(t, pool, uniqueClientID())

	found, err := repository.FindByInstanceIDs(context.Background(), []uuid.UUID{existing.ID, withoutSettings.ID, uuid.New()})

	requireNoError(t, err)
	if len(found) != 1 {
		t.Fatalf("expected 1 settings, got %d", len(found))
	}
	if _, present := found[existing.ID]; !present {
		t.Fatalf("settings of existing instance missing")
	}
}

func TestSettingsFindByInstanceIDsEmptyInput(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceSettingsRepository(pool)

	tests := []struct {
		name string
		ids  []uuid.UUID
	}{
		{name: "empty slice", ids: []uuid.UUID{}},
		{name: "nil slice", ids: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := repository.FindByInstanceIDs(context.Background(), test.ids)

			requireNoError(t, err)
			if len(found) != 0 {
				t.Fatalf("expected no settings, got %d", len(found))
			}
		})
	}
}

func TestChatFindJIDsByIDsReturnsOnlyRequestedInstanceChats(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	owner := newInstance(t, pool, uniqueClientID())
	other := newInstance(t, pool, uniqueClientID())
	firstChat := newChat(t, pool, owner.ID, "first-"+uuid.NewString()+"@s.whatsapp.net")
	secondChat := newChat(t, pool, owner.ID, "second-"+uuid.NewString()+"@s.whatsapp.net")
	notRequestedChat := newChat(t, pool, owner.ID, "unrequested-"+uuid.NewString()+"@s.whatsapp.net")
	otherInstanceChat := newChat(t, pool, other.ID, "other-"+uuid.NewString()+"@s.whatsapp.net")

	found, err := repository.FindJIDsByIDs(context.Background(), owner.ID,
		[]uuid.UUID{firstChat.ID, secondChat.ID, otherInstanceChat.ID, uuid.New()})

	requireNoError(t, err)
	if len(found) != 2 {
		t.Fatalf("expected 2 jids, got %d: %v", len(found), found)
	}
	if found[firstChat.ID] != firstChat.JID || found[secondChat.ID] != secondChat.JID {
		t.Fatalf("unexpected jids %v", found)
	}
	if _, present := found[otherInstanceChat.ID]; present {
		t.Fatalf("chat of another instance was returned")
	}
	if _, present := found[notRequestedChat.ID]; present {
		t.Fatalf("chat that was not requested was returned")
	}
}

func TestChatFindJIDsByIDsEmptyInput(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewChatRepository(pool)
	owner := newInstance(t, pool, uniqueClientID())
	newChat(t, pool, owner.ID, "chat-"+uuid.NewString()+"@s.whatsapp.net")

	tests := []struct {
		name string
		ids  []uuid.UUID
	}{
		{name: "empty slice", ids: []uuid.UUID{}},
		{name: "nil slice", ids: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := repository.FindJIDsByIDs(context.Background(), owner.ID, test.ids)

			requireNoError(t, err)
			if len(found) != 0 {
				t.Fatalf("expected no jids, got %d", len(found))
			}
		})
	}
}

func TestMessageFindReplyTargetsMatchesChatAndWAMessageID(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMessageRepository(pool)
	owner := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, owner.ID, "chat-"+uuid.NewString()+"@s.whatsapp.net")
	first := insertMessage(t, pool, newMessage(owner.ID, chat.ID, "sent", microsecondsNow()))
	second := insertMessage(t, pool, newMessage(owner.ID, chat.ID, "sent", microsecondsNow()))
	unrelated := insertMessage(t, pool, newMessage(owner.ID, chat.ID, "sent", microsecondsNow()))
	firstRef := domain.MessageRef{ChatID: chat.ID, WAMessageID: first.WAMessageID}
	secondRef := domain.MessageRef{ChatID: chat.ID, WAMessageID: second.WAMessageID}
	missingRef := domain.MessageRef{ChatID: chat.ID, WAMessageID: "wa-" + uuid.NewString()}

	found, err := repository.FindReplyTargets(context.Background(), owner.ID,
		[]domain.MessageRef{firstRef, secondRef, missingRef})

	requireNoError(t, err)
	if len(found) != 2 {
		t.Fatalf("expected 2 targets, got %d: %v", len(found), found)
	}
	if found[firstRef] != first.ID || found[secondRef] != second.ID {
		t.Fatalf("unexpected targets %v", found)
	}
	if _, present := found[domain.MessageRef{ChatID: chat.ID, WAMessageID: unrelated.WAMessageID}]; present {
		t.Fatalf("message that was not requested was returned")
	}
}

func TestMessageFindReplyTargetsDoesNotMatchOtherChatWithSameWAMessageID(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMessageRepository(pool)
	owner := newInstance(t, pool, uniqueClientID())
	chat := newChat(t, pool, owner.ID, "chat-"+uuid.NewString()+"@s.whatsapp.net")
	otherChat := newChat(t, pool, owner.ID, "other-"+uuid.NewString()+"@s.whatsapp.net")
	inChat := insertMessage(t, pool, newMessage(owner.ID, chat.ID, "sent", microsecondsNow()))
	inOtherChat := newMessage(owner.ID, otherChat.ID, "sent", microsecondsNow())
	inOtherChat.WAMessageID = inChat.WAMessageID
	insertMessage(t, pool, inOtherChat)

	found, err := repository.FindReplyTargets(context.Background(), owner.ID,
		[]domain.MessageRef{{ChatID: chat.ID, WAMessageID: inChat.WAMessageID}})

	requireNoError(t, err)
	if len(found) != 1 {
		t.Fatalf("expected 1 target, got %d: %v", len(found), found)
	}
	for _, id := range found {
		if id != inChat.ID {
			t.Fatalf("expected message %s, got %s", inChat.ID, id)
		}
	}
}

func TestMessageFindReplyTargetsDoesNotMatchOtherInstance(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMessageRepository(pool)
	owner := newInstance(t, pool, uniqueClientID())
	other := newInstance(t, pool, uniqueClientID())
	otherChat := newChat(t, pool, other.ID, "chat-"+uuid.NewString()+"@s.whatsapp.net")
	otherMessage := insertMessage(t, pool, newMessage(other.ID, otherChat.ID, "sent", microsecondsNow()))

	found, err := repository.FindReplyTargets(context.Background(), owner.ID,
		[]domain.MessageRef{{ChatID: otherChat.ID, WAMessageID: otherMessage.WAMessageID}})

	requireNoError(t, err)
	if len(found) != 0 {
		t.Fatalf("expected no targets, got %v", found)
	}
}

func TestMessageFindReplyTargetsEmptyInput(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewMessageRepository(pool)
	owner := newInstance(t, pool, uniqueClientID())

	tests := []struct {
		name       string
		references []domain.MessageRef
	}{
		{name: "empty slice", references: []domain.MessageRef{}},
		{name: "nil slice", references: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := repository.FindReplyTargets(context.Background(), owner.ID, test.references)

			requireNoError(t, err)
			if len(found) != 0 {
				t.Fatalf("expected no targets, got %v", found)
			}
		})
	}
}

func TestInstanceUnitOfWorkCommitsInstanceAndSettings(t *testing.T) {
	pool := newTestPool(t)
	unitOfWork := postgres.NewInstanceUnitOfWork(pool)
	clientID := uniqueClientID()
	created := &domain.Instance{ID: uuid.New(), ClientID: clientID, Name: "instance", Status: domain.InstanceStatusCreated}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE id = $1`, created.ID)
	})

	err := unitOfWork.Do(context.Background(), func(unit instance.Unit) error {
		if err := unit.Instances.Insert(context.Background(), created); err != nil {
			return err
		}
		return unit.Settings.Insert(context.Background(), &domain.InstanceSettings{InstanceID: created.ID, SyncHistory: true})
	})

	requireNoError(t, err)
	foundInstance, err := postgres.NewInstanceRepository(pool).FindByID(context.Background(), clientID, created.ID)
	requireNoError(t, err)
	if foundInstance.ID != created.ID {
		t.Fatalf("unexpected instance %+v", foundInstance)
	}
	foundSettings, err := postgres.NewInstanceSettingsRepository(pool).FindByInstanceID(context.Background(), created.ID)
	requireNoError(t, err)
	if !foundSettings.SyncHistory {
		t.Fatalf("unexpected settings %+v", foundSettings)
	}
}

func TestInstanceUnitOfWorkRollsBackInstanceWhenSettingsInsertFails(t *testing.T) {
	pool := newTestPool(t)
	unitOfWork := postgres.NewInstanceUnitOfWork(pool)
	clientID := uniqueClientID()
	created := &domain.Instance{ID: uuid.New(), ClientID: clientID, Name: "instance", Status: domain.InstanceStatusCreated}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE id = $1`, created.ID)
	})

	err := unitOfWork.Do(context.Background(), func(unit instance.Unit) error {
		if err := unit.Instances.Insert(context.Background(), created); err != nil {
			return err
		}
		return unit.Settings.Insert(context.Background(), &domain.InstanceSettings{InstanceID: uuid.New()})
	})

	if err == nil {
		t.Fatalf("expected settings insert to fail")
	}
	_, findErr := postgres.NewInstanceRepository(pool).FindByID(context.Background(), clientID, created.ID)
	requireAppError(t, findErr, domain.InstanceNotFound())
}
