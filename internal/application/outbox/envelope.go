package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type instanceCache map[uuid.UUID]*domain.EnvelopeInstance

func (c instanceCache) resolve(ctx context.Context, reader InstanceReader, id uuid.UUID) (*domain.EnvelopeInstance, error) {
	if cached, found := c[id]; found {
		return cached, nil
	}
	resolved := &domain.EnvelopeInstance{ID: id}
	instance, err := reader.FindUnscoped(ctx, id)
	switch {
	case err == nil:
		resolved.ExternalID = instance.ExternalID
		resolved.Phone = instance.Phone
	case !isInstanceNotFound(err):
		return nil, err
	}
	c[id] = resolved
	return resolved, nil
}

func isInstanceNotFound(err error) bool {
	appErr, ok := domain.AsAppError(err)
	return ok && appErr.Code == domain.InstanceNotFound().Code
}

func buildEnvelope(event domain.OutboxEvent, instance *domain.EnvelopeInstance) ([]byte, error) {
	body, err := json.Marshal(domain.EventEnvelope{
		EventID:      event.EventID,
		EventType:    event.EventType,
		EventVersion: int(event.EventVersion),
		Sequence:     event.Seq,
		OccurredAt:   event.OccurredAt.UTC(),
		Instance:     instance,
		Data:         event.Payload,
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: encode envelope: %w", err)
	}
	return body, nil
}

func kafkaMessage(event domain.OutboxEvent, body []byte) Message {
	headers := []Header{
		{Key: "event_id", Value: event.EventID.String()},
		{Key: "event_type", Value: event.EventType},
		{Key: "event_version", Value: strconv.Itoa(int(event.EventVersion))},
	}
	if event.InstanceID != nil {
		headers = append(headers, Header{Key: "instance_id", Value: event.InstanceID.String()})
	}
	return Message{Topic: event.EventType, Key: event.PartitionKey, Value: body, Headers: headers}
}
