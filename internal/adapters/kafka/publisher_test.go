package kafka

import (
	"context"
	"net"
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

func TestPingSucceedsWhenAnyBrokerAcceptsConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := closed.Addr().String()
	closed.Close()

	publisher := NewPublisher([]string{deadAddress, listener.Addr().String()})
	if err := publisher.Ping(context.Background()); err != nil {
		t.Fatalf("expected ping to succeed: %v", err)
	}
}

func TestPingFailsWhenNoBrokerIsReachable(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := closed.Addr().String()
	closed.Close()

	publisher := NewPublisher([]string{deadAddress})
	if err := publisher.Ping(context.Background()); err == nil {
		t.Fatal("expected ping to fail")
	}
}
