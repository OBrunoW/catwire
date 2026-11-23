package webhook

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func dueDelivery(attempts int) domain.DueDelivery {
	return domain.DueDelivery{
		WebhookDelivery: domain.WebhookDelivery{
			ID: uuid.New(), WebhookID: uuid.New(), EventID: uuid.New(), EventType: "whatsapp.message.received",
			Payload: []byte(`{"event_type":"whatsapp.message.received"}`), Attempts: attempts,
		},
		WebhookURL:    "https://consumer.example/hook",
		WebhookSecret: "secret",
	}
}

func newDispatcherForTest(store *fakeDispatchStore, sender *fakeSender, maxAttempts int) *Dispatcher {
	return NewDispatcher(DispatcherOptions{
		Store: store, Sender: sender, MaxAttempts: maxAttempts, Now: func() time.Time { return fixedNow },
	})
}

func TestDispatcherRecordsSuccessOnAny2xx(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent} {
		delivery := dueDelivery(0)
		store := newFakeDispatchStore(delivery)
		sender := &fakeSender{status: status}

		claimed, err := newDispatcherForTest(store, sender, 10).RunOnce(context.Background())

		if err != nil || claimed != 1 {
			t.Fatalf("claimed=%d err=%v", claimed, err)
		}
		if store.success[delivery.ID] != status || len(store.failures) != 0 {
			t.Fatalf("status %d must be success", status)
		}
	}
}

func TestDispatcherSendsRequestWithDeliveryData(t *testing.T) {
	delivery := dueDelivery(0)
	sender := &fakeSender{status: http.StatusOK}

	if _, err := newDispatcherForTest(newFakeDispatchStore(delivery), sender, 10).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	request := sender.requests[0]
	if request.URL != delivery.WebhookURL || request.Secret != delivery.WebhookSecret || request.DeliveryID != delivery.ID ||
		request.EventID != delivery.EventID || string(request.Body) != string(delivery.Payload) || !request.Timestamp.Equal(fixedNow) {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestDispatcherNon2xxSchedulesBackoff(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusFound} {
		delivery := dueDelivery(0)
		store := newFakeDispatchStore(delivery)

		if _, err := newDispatcherForTest(store, &fakeSender{status: status}, 10).RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}

		failure, recorded := store.failures[delivery.ID]
		if !recorded || failure.statusCode == nil || *failure.statusCode != status {
			t.Fatalf("status %d must be recorded as failure: %+v", status, failure)
		}
		if !failure.next.Equal(fixedNow.Add(30*time.Second)) || failure.status != domain.DeliveryStatusFailed {
			t.Fatalf("unexpected failure state: %+v", failure)
		}
	}
}

func TestDispatcherTransportErrorRecordsFailureWithoutStatus(t *testing.T) {
	delivery := dueDelivery(0)
	store := newFakeDispatchStore(delivery)
	sender := &fakeSender{err: errors.New("request timed out")}

	if _, err := newDispatcherForTest(store, sender, 10).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	failure := store.failures[delivery.ID]
	if failure.statusCode != nil || failure.message != "request timed out" {
		t.Fatalf("unexpected failure: %+v", failure)
	}
}

func TestDispatcherBackoffFollowsSchedule(t *testing.T) {
	expected := []time.Duration{
		30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour,
		2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 12 * time.Hour,
	}
	for attempts, wait := range expected {
		delivery := dueDelivery(attempts)
		store := newFakeDispatchStore(delivery)

		if _, err := newDispatcherForTest(store, &fakeSender{status: 500}, 20).RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}

		if got := store.failures[delivery.ID].next.Sub(fixedNow); got != wait {
			t.Fatalf("attempt %d: expected %s, got %s", attempts+1, wait, got)
		}
	}
}

func TestDispatcherBecomesDeadAtMaxAttempts(t *testing.T) {
	delivery := dueDelivery(9)
	store := newFakeDispatchStore(delivery)

	if _, err := newDispatcherForTest(store, &fakeSender{status: 500}, 10).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if store.failures[delivery.ID].status != domain.DeliveryStatusDead {
		t.Fatalf("expected dead, got %s", store.failures[delivery.ID].status)
	}
}

func TestDispatcherProcessesWholeBatch(t *testing.T) {
	first, second := dueDelivery(0), dueDelivery(0)
	store := newFakeDispatchStore(first, second)

	claimed, err := newDispatcherForTest(store, &fakeSender{status: 200}, 10).RunOnce(context.Background())

	if err != nil || claimed != 2 || len(store.success) != 2 {
		t.Fatalf("claimed=%d success=%d err=%v", claimed, len(store.success), err)
	}
}
