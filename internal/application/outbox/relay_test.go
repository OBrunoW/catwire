package outbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func newRelayForTest(db *fakeDB, publisher Publisher) *Relay {
	return NewRelay(RelayOptions{Unit: db, Publisher: publisher, Now: func() time.Time { return db.now }})
}

func TestRelayPublishesWithKeyHeadersAndEnvelope(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	event := db.addEvent(instance.ID, instance.ID.String()+":5511@s.whatsapp.net")
	publisher := &fakePublisher{}

	fetched, err := newRelayForTest(db, publisher).RunOnce(context.Background())

	if err != nil || fetched != 1 {
		t.Fatalf("fetched=%d err=%v", fetched, err)
	}
	if len(publisher.messages) != 1 {
		t.Fatalf("expected one message, got %d", len(publisher.messages))
	}
	message := publisher.messages[0]
	if message.Topic != "whatsapp.message.received" || message.Key != event.PartitionKey {
		t.Fatalf("unexpected topic or key: %s %s", message.Topic, message.Key)
	}
	headers := map[string]string{}
	for _, header := range message.Headers {
		headers[header.Key] = header.Value
	}
	if headers["event_id"] != event.EventID.String() || headers["event_type"] != event.EventType ||
		headers["event_version"] != "1" || headers["instance_id"] != instance.ID.String() {
		t.Fatalf("unexpected headers: %v", headers)
	}
	var envelope domain.EventEnvelope
	if err := json.Unmarshal(message.Value, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.EventID != event.EventID || envelope.Sequence != event.Seq || envelope.Instance == nil ||
		envelope.Instance.ID != instance.ID || *envelope.Instance.ExternalID != "crm-user-42" || *envelope.Instance.Phone != "5511999999999" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if string(envelope.Data) != string(event.Payload) {
		t.Fatalf("unexpected data: %s", envelope.Data)
	}
	if event.KafkaPublishedAt == nil || event.DispatchedAt == nil {
		t.Fatal("event must be marked published and dispatched")
	}
}

func TestRelayKafkaFailureDoesNotMarkPublishedAndSchedulesBackoff(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	event := db.addEvent(instance.ID, "key-a")
	publisher := &fakePublisher{failOn: map[string]error{"key-a": errBrokerDown}}
	db.webhooks = append(db.webhooks, domain.Webhook{ID: uuid.New(), ClientID: "client-a", Active: true})

	if _, err := newRelayForTest(db, publisher).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if event.KafkaPublishedAt != nil || event.DispatchedAt != nil {
		t.Fatal("failed publish must not mark the event")
	}
	if event.Attempts != 1 || event.LastError == nil || !event.NextAttemptAt.Equal(fixedNow.Add(30*time.Second)) {
		t.Fatalf("unexpected failure bookkeeping: attempts=%d next=%s", event.Attempts, event.NextAttemptAt)
	}
	if len(db.deliveries) != 0 {
		t.Fatal("no delivery may be created before Kafka accepted the event")
	}
}

func TestRelayKeepsPartitionOrderAcrossFailure(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	first := db.addEvent(instance.ID, "key-a")
	second := db.addEvent(instance.ID, "key-a")
	other := db.addEvent(instance.ID, "key-b")
	publisher := &fakePublisher{failOn: map[string]error{"key-a": errBrokerDown}}
	relay := newRelayForTest(db, publisher)

	if _, err := relay.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if other.DispatchedAt == nil {
		t.Fatal("other partition must not be blocked")
	}
	if first.DispatchedAt != nil || second.DispatchedAt != nil || len(publisher.messages) != 1 {
		t.Fatal("second event of the partition must wait for the first")
	}

	publisher.failOn = nil
	db.now = db.now.Add(time.Minute)
	for range 2 {
		if _, err := relay.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if first.DispatchedAt == nil || second.DispatchedAt == nil {
		t.Fatal("both events must be dispatched after recovery")
	}
	order := []uuid.UUID{}
	for _, message := range publisher.messages {
		if message.Key != "key-a" {
			continue
		}
		var envelope domain.EventEnvelope
		if err := json.Unmarshal(message.Value, &envelope); err != nil {
			t.Fatal(err)
		}
		order = append(order, envelope.EventID)
	}
	if len(order) != 2 || order[0] != first.EventID || order[1] != second.EventID {
		t.Fatalf("partition order broken: %v", order)
	}
}

func TestRelayCreatesDeliveriesOnlyForMatchingWebhooks(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	event := db.addEvent(instance.ID, "key-a")
	matching := domain.Webhook{ID: uuid.New(), ClientID: "client-a", Active: true, Events: []string{"whatsapp.message.received"}}
	allEvents := domain.Webhook{ID: uuid.New(), ClientID: "client-a", Active: true}
	otherEvent := domain.Webhook{ID: uuid.New(), ClientID: "client-a", Active: true, Events: []string{"whatsapp.media.stored"}}
	otherClient := domain.Webhook{ID: uuid.New(), ClientID: "client-b", Active: true}
	inactive := domain.Webhook{ID: uuid.New(), ClientID: "client-a", Active: false}
	db.webhooks = []domain.Webhook{matching, allEvents, otherEvent, otherClient, inactive}

	if _, err := newRelayForTest(db, &fakePublisher{}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(db.deliveries) != 2 {
		t.Fatalf("expected 2 deliveries, got %d", len(db.deliveries))
	}
	for _, delivery := range db.deliveries {
		if delivery.EventID != event.EventID || delivery.EventType != event.EventType {
			t.Fatalf("unexpected delivery: %+v", delivery)
		}
		var envelope domain.EventEnvelope
		if err := json.Unmarshal(delivery.Payload, &envelope); err != nil || envelope.EventID != event.EventID {
			t.Fatalf("delivery payload must be the envelope: %s", delivery.Payload)
		}
	}
}

func TestRelayWithoutPublisherDispatchesToWebhooksOnly(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	event := db.addEvent(instance.ID, "key-a")
	db.webhooks = []domain.Webhook{{ID: uuid.New(), ClientID: "client-a", Active: true}}

	if _, err := newRelayForTest(db, nil).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if event.DispatchedAt == nil || event.KafkaPublishedAt != nil || len(db.deliveries) != 1 {
		t.Fatalf("unexpected state: dispatched=%v kafka=%v deliveries=%d", event.DispatchedAt, event.KafkaPublishedAt, len(db.deliveries))
	}
}

func TestRelaySkipsKafkaWhenAlreadyPublished(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	event := db.addEvent(instance.ID, "key-a")
	already := fixedNow
	event.KafkaPublishedAt = &already
	publisher := &fakePublisher{}

	if _, err := newRelayForTest(db, publisher).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(publisher.messages) != 0 || event.DispatchedAt == nil {
		t.Fatal("must dispatch without publishing again")
	}
}

func TestRelayEmitsEnvelopeWithoutInstanceDetailsWhenInstanceIsGone(t *testing.T) {
	db := newFakeDB()
	event := db.addEvent(uuid.New(), "key-a")
	publisher := &fakePublisher{}

	if _, err := newRelayForTest(db, publisher).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	var envelope domain.EventEnvelope
	if err := json.Unmarshal(publisher.messages[0].Value, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Instance == nil || envelope.Instance.ID != *event.InstanceID || envelope.Instance.ExternalID != nil {
		t.Fatalf("unexpected instance: %+v", envelope.Instance)
	}
	if event.DispatchedAt == nil {
		t.Fatal("event of a removed instance must not block the queue")
	}
}

func TestRelayWithNothingPendingFetchesZero(t *testing.T) {
	fetched, err := newRelayForTest(newFakeDB(), &fakePublisher{}).RunOnce(context.Background())
	if err != nil || fetched != 0 {
		t.Fatalf("fetched=%d err=%v", fetched, err)
	}
}

func TestRelayPublishTimeoutFailsEventAndContinues(t *testing.T) {
	db := newFakeDB()
	instance := db.addInstance()
	event := db.addEvent(instance.ID, "key-a")
	relay := NewRelay(RelayOptions{
		Unit: db, Publisher: blockingPublisher{}, PublishTimeout: 20 * time.Millisecond,
		Now: func() time.Time { return db.now },
	})

	fetched, err := relay.RunOnce(context.Background())

	if err != nil || fetched != 1 {
		t.Fatalf("fetched=%d err=%v", fetched, err)
	}
	if event.KafkaPublishedAt != nil || event.Attempts != 1 || event.LastError == nil {
		t.Fatalf("timeout must record a failure: attempts=%d", event.Attempts)
	}
}
