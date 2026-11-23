package kafka

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/OBrunoW/catwire/internal/application/outbox"
)

const brokerTestTimeout = 20 * time.Second

func brokersFromEnvironment(t *testing.T) []string {
	t.Helper()
	value := os.Getenv("KAFKA_TEST_BROKERS")
	if value == "" {
		t.Skip("KAFKA_TEST_BROKERS not set")
	}
	return strings.Split(value, ",")
}

func createTopic(t *testing.T, broker, topic string) {
	t.Helper()
	connection, err := kafkago.Dial("tcp", broker)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer connection.Close()
	config := kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}
	if err := connection.CreateTopics(config); err != nil {
		t.Fatalf("create topic: %v", err)
	}
}

func TestPublisherDeliversKeyValueAndHeadersToRealBroker(t *testing.T) {
	brokers := brokersFromEnvironment(t)
	topic := "catwire.acceptance." + uuid.NewString()
	createTopic(t, brokers[0], topic)
	publisher := NewPublisher(brokers)
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(context.Background(), brokerTestTimeout)
	defer cancel()
	message := outbox.Message{
		Topic:   topic,
		Key:     "instance:chat",
		Value:   []byte(`{"event_type":"whatsapp.message.received"}`),
		Headers: []outbox.Header{{Key: "event_id", Value: "event-1"}},
	}

	if err := publisher.Publish(ctx, message); err != nil {
		t.Fatalf("publish: %v", err)
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer reader.Close()
	received, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(received.Key) != message.Key || string(received.Value) != string(message.Value) {
		t.Fatalf("unexpected message: key=%s value=%s", received.Key, received.Value)
	}
	if len(received.Headers) != 1 || received.Headers[0].Key != "event_id" || string(received.Headers[0].Value) != "event-1" {
		t.Fatalf("unexpected headers: %+v", received.Headers)
	}
}
