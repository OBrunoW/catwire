package kafka

import (
	"testing"

	"github.com/OBrunoW/catwire/internal/application/outbox"
)

func TestToKafkaMessageMapsTopicKeyValueAndHeaders(t *testing.T) {
	message := toKafkaMessage(outbox.Message{
		Topic:   "whatsapp.message.received",
		Key:     "instance:chat",
		Value:   []byte(`{"a":1}`),
		Headers: []outbox.Header{{Key: "event_id", Value: "e1"}, {Key: "instance_id", Value: "i1"}},
	})

	if message.Topic != "whatsapp.message.received" || string(message.Key) != "instance:chat" || string(message.Value) != `{"a":1}` {
		t.Fatalf("unexpected message: %+v", message)
	}
	if len(message.Headers) != 2 || message.Headers[0].Key != "event_id" || string(message.Headers[1].Value) != "i1" {
		t.Fatalf("unexpected headers: %+v", message.Headers)
	}
}
