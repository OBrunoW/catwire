package postgres_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/OBrunoW/catwire/internal/adapters/postgres"
	adapterwebhook "github.com/OBrunoW/catwire/internal/adapters/webhook"
	"github.com/OBrunoW/catwire/internal/application/outbox"
	appwebhook "github.com/OBrunoW/catwire/internal/application/webhook"
	"github.com/OBrunoW/catwire/internal/domain"
)

type capturingPublisher struct {
	mu       sync.Mutex
	messages []outbox.Message
}

func (p *capturingPublisher) Publish(_ context.Context, message outbox.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, message)
	return nil
}

func (p *capturingPublisher) messageForEvent(eventID string) (outbox.Message, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, message := range p.messages {
		for _, header := range message.Headers {
			if header.Key == "event_id" && header.Value == eventID {
				return message, true
			}
		}
	}
	return outbox.Message{}, false
}

type receivedWebhook struct {
	signature string
	timestamp string
	eventID   string
	body      []byte
}

type webhookReceiver struct {
	mu       sync.Mutex
	received []receivedWebhook
}

func (r *webhookReceiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	r.mu.Lock()
	r.received = append(r.received, receivedWebhook{
		signature: request.Header.Get("X-Catwire-Signature"),
		timestamp: request.Header.Get("X-Catwire-Timestamp"),
		eventID:   request.Header.Get("X-Catwire-Event-Id"),
		body:      body,
	})
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (r *webhookReceiver) deliveryForEvent(eventID string) (receivedWebhook, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, delivery := range r.received {
		if delivery.eventID == eventID {
			return delivery, true
		}
	}
	return receivedWebhook{}, false
}

func designSignature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + string(body)))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

var envelopeFields = []string{"event_id", "event_type", "event_version", "sequence", "occurred_at", "instance", "data"}

func decodeEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("envelope is not valid JSON: %v: %s", err, raw)
	}
	return envelope
}

func pointToReceiver(t *testing.T, pool *pgxpool.Pool, clientID string, hook *domain.Webhook, url string) {
	t.Helper()
	update := domain.WebhookUpdate{URL: &url}
	_, err := postgres.NewWebhookRepository(pool).Update(context.Background(), clientID, hook.ID, update)
	requireNoError(t, err)
}

func TestEventLeavesOutboxToKafkaAndSignedWebhookWithSameEnvelope(t *testing.T) {
	pool := newTestPool(t)
	clientID := uniqueClientID()
	publisher := &capturingPublisher{}
	relay := newRelayFixture(t, pool, clientID, publisher, 0)
	instance := newInstance(t, pool, clientID)
	hook := newWebhook(t, pool, clientID, nil, nil)
	receiver := &webhookReceiver{}
	server := httptest.NewServer(receiver)
	defer server.Close()
	pointToReceiver(t, pool, clientID, hook, server.URL)
	event := insertRelayEvent(t, pool, clientID, clientID+":chat", &instance.ID)
	repository := postgres.NewWebhookDeliveryRepository(pool)
	sender := adapterwebhook.NewSender(adapterwebhook.Options{Timeout: 2 * time.Second, AllowPrivate: true})
	dispatcher := appwebhook.NewDispatcher(appwebhook.DispatcherOptions{Store: repository, Sender: sender, BatchSize: relayBatchSize})

	_, relayErr := relay.RunOnce(context.Background())
	_, dispatchErr := dispatcher.RunOnce(context.Background())

	requireNoError(t, relayErr)
	requireNoError(t, dispatchErr)
	kafkaMessage, published := publisher.messageForEvent(event.EventID.String())
	if !published {
		t.Fatal("expected the event to be published to Kafka")
	}
	if kafkaMessage.Topic != event.EventType || kafkaMessage.Key != event.PartitionKey {
		t.Fatalf("expected topic %q and key %q, got %q and %q", event.EventType, event.PartitionKey, kafkaMessage.Topic, kafkaMessage.Key)
	}
	delivery, delivered := receiver.deliveryForEvent(event.EventID.String())
	if !delivered {
		t.Fatal("expected the webhook to receive the event")
	}
	webhookEnvelope := decodeEnvelope(t, delivery.body)
	kafkaEnvelope := decodeEnvelope(t, kafkaMessage.Value)
	for _, field := range envelopeFields {
		webhookValue, inWebhook := webhookEnvelope[field]
		kafkaValue, inKafka := kafkaEnvelope[field]
		if !inWebhook || !inKafka {
			t.Fatalf("envelope field %q missing: webhook=%t kafka=%t", field, inWebhook, inKafka)
		}
		if !reflect.DeepEqual(webhookValue, kafkaValue) {
			t.Fatalf("envelope field %q differs: webhook=%v kafka=%v", field, webhookValue, kafkaValue)
		}
	}
	if !reflect.DeepEqual(webhookEnvelope, kafkaEnvelope) {
		t.Fatalf("webhook envelope differs from the Kafka envelope:\nwebhook: %s\nkafka: %s", delivery.body, kafkaMessage.Value)
	}
	if delivery.signature != designSignature(hook.Secret, delivery.timestamp, delivery.body) {
		t.Fatalf("webhook signature does not verify: %s", delivery.signature)
	}
}
