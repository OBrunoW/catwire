package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	"github.com/OBrunoW/catwire/internal/domain"
)

func TestInstanceInsertAndFindByID(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	clientID := uniqueClientID()
	instance := newInstance(t, pool, clientID)

	found, err := repository.FindByID(context.Background(), clientID, instance.ID)

	requireNoError(t, err)
	if found.ID != instance.ID || found.ClientID != clientID {
		t.Fatalf("unexpected instance %+v", found)
	}
	if found.Status != domain.InstanceStatusCreated || found.CreatedAt.IsZero() {
		t.Fatalf("unexpected defaults %+v", found)
	}
}

func TestInstanceFindByIDNotFound(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())

	tests := []struct {
		name     string
		clientID string
		id       uuid.UUID
	}{
		{name: "other client", clientID: uniqueClientID(), id: instance.ID},
		{name: "unknown id", clientID: instance.ClientID, id: uuid.New()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := repository.FindByID(context.Background(), test.clientID, test.id)

			requireAppError(t, err, domain.InstanceNotFound())
		})
	}
}

func TestInstanceFindByIDIgnoresSoftDeleted(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())
	requireNoError(t, repository.SoftDelete(context.Background(), instance.ClientID, instance.ID))

	_, err := repository.FindByID(context.Background(), instance.ClientID, instance.ID)

	requireAppError(t, err, domain.InstanceNotFound())
}

func TestInstanceInsertDuplicateExternalID(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	clientID := uniqueClientID()
	externalID := stringPointer("external-" + uuid.NewString())
	first := &domain.Instance{ID: uuid.New(), ClientID: clientID, ExternalID: externalID, Name: "first", Status: domain.InstanceStatusCreated}
	requireNoError(t, repository.Insert(context.Background(), first))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE id = $1`, first.ID)
	})

	tests := []struct {
		name     string
		clientID string
		expected *domain.AppError
	}{
		{name: "same client", clientID: clientID, expected: domain.InstanceAlreadyExists()},
		{name: "other client", clientID: uniqueClientID(), expected: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			second := &domain.Instance{ID: uuid.New(), ClientID: test.clientID, ExternalID: externalID, Name: "second", Status: domain.InstanceStatusCreated}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE id = $1`, second.ID)
			})

			err := repository.Insert(context.Background(), second)

			if test.expected == nil {
				requireNoError(t, err)
				return
			}
			requireAppError(t, err, test.expected)
		})
	}
}

func TestInstanceExternalIDReusableAfterSoftDelete(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	clientID := uniqueClientID()
	externalID := stringPointer("external-" + uuid.NewString())
	first := &domain.Instance{ID: uuid.New(), ClientID: clientID, ExternalID: externalID, Name: "first", Status: domain.InstanceStatusCreated}
	requireNoError(t, repository.Insert(context.Background(), first))
	requireNoError(t, repository.SoftDelete(context.Background(), clientID, first.ID))
	second := &domain.Instance{ID: uuid.New(), ClientID: clientID, ExternalID: externalID, Name: "second", Status: domain.InstanceStatusCreated}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM catwire.instances WHERE client_id = $1`, clientID)
	})

	err := repository.Insert(context.Background(), second)

	requireNoError(t, err)
}

func TestInstanceUpdateStatusReturnsPreviousStatus(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	instance := newInstance(t, pool, uniqueClientID())

	previous, err := repository.UpdateStatus(context.Background(), instance.ID, domain.InstanceStatusChange{Status: domain.InstanceStatusConnected})

	requireNoError(t, err)
	if previous != domain.InstanceStatusCreated {
		t.Fatalf("expected previous status created, got %s", previous)
	}
	found, err := repository.FindByID(context.Background(), instance.ClientID, instance.ID)
	requireNoError(t, err)
	if found.Status != domain.InstanceStatusConnected {
		t.Fatalf("expected connected, got %s", found.Status)
	}
}

func TestInstanceListByClientPaginatesWithoutRepeating(t *testing.T) {
	pool := newTestPool(t)
	repository := postgres.NewInstanceRepository(pool)
	clientID := uniqueClientID()
	for range 3 {
		newInstance(t, pool, clientID)
	}

	first, err := repository.ListByClient(context.Background(), clientID, domain.InstanceFilter{}, apihttp.PageParams{Limit: 2})
	requireNoError(t, err)
	if len(first.Data) != 2 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("unexpected first page %+v", first)
	}
	second, err := repository.ListByClient(context.Background(), clientID, domain.InstanceFilter{}, apihttp.PageParams{Limit: 2, Cursor: *first.NextCursor})

	requireNoError(t, err)
	if len(second.Data) != 1 || second.HasMore {
		t.Fatalf("unexpected second page %+v", second)
	}
	for _, firstItem := range first.Data {
		if firstItem.ID == second.Data[0].ID {
			t.Fatalf("instance %s repeated across pages", firstItem.ID)
		}
	}
}
