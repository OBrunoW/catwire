package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/OBrunoW/catwire/internal/application/outbox"
)

const (
	batchTimeout = 5 * time.Millisecond
	pingTimeout  = 2 * time.Second
)

var errNoBrokerReachable = errors.New("kafka: no broker reachable")

type Publisher struct {
	writer  *kafkago.Writer
	brokers []string
}

func NewPublisher(brokers []string) *Publisher {
	return &Publisher{brokers: brokers, writer: &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Balancer:               &kafkago.Hash{},
		RequiredAcks:           kafkago.RequireAll,
		AllowAutoTopicCreation: false,
		BatchTimeout:           batchTimeout,
	}}
}

func (p *Publisher) Publish(ctx context.Context, message outbox.Message) error {
	if err := p.writer.WriteMessages(ctx, toKafkaMessage(message)); err != nil {
		return fmt.Errorf("kafka write to %s: %w", message.Topic, err)
	}
	return nil
}

func (p *Publisher) Ping(ctx context.Context) error {
	dialer := &kafkago.Dialer{Timeout: pingTimeout}
	for _, broker := range p.brokers {
		connection, err := dialer.DialContext(ctx, "tcp", broker)
		if err != nil {
			continue
		}
		if err := connection.Close(); err != nil {
			return fmt.Errorf("kafka close ping connection: %w", err)
		}
		return nil
	}
	return errNoBrokerReachable
}

func (p *Publisher) Close() error {
	return p.writer.Close()
}

func toKafkaMessage(message outbox.Message) kafkago.Message {
	headers := make([]kafkago.Header, 0, len(message.Headers))
	for _, header := range message.Headers {
		headers = append(headers, kafkago.Header{Key: header.Key, Value: []byte(header.Value)})
	}
	return kafkago.Message{
		Topic:   message.Topic,
		Key:     []byte(message.Key),
		Value:   message.Value,
		Headers: headers,
	}
}

var _ outbox.Publisher = (*Publisher)(nil)
