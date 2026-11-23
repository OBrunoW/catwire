package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func (r *MessageRepository) ListInboundReceived(ctx context.Context, instanceID, chatID uuid.UUID) ([]domain.Message, error) {
	return r.listMany(ctx, "list inbound received messages",
		`instance_id = $1 AND chat_id = $2 AND direction = 'inbound' AND status = 'received' ORDER BY wa_timestamp, id`,
		instanceID, chatID,
	)
}

func (r *MessageRepository) FindByChatAndIDs(ctx context.Context, instanceID, chatID uuid.UUID, ids []uuid.UUID) ([]domain.Message, error) {
	return r.listMany(ctx, "find messages by chat and ids",
		`instance_id = $1 AND chat_id = $2 AND id = ANY($3) ORDER BY wa_timestamp, id`,
		instanceID, chatID, orEmptyUUIDs(ids),
	)
}

func (r *MessageRepository) FindOldestInChat(ctx context.Context, instanceID, chatID uuid.UUID) (*domain.Message, error) {
	return r.findOne(ctx, "find oldest message in chat",
		`instance_id = $1 AND chat_id = $2 ORDER BY wa_timestamp, id LIMIT 1`,
		instanceID, chatID,
	)
}

func (r *MessageRepository) listMany(ctx context.Context, operation, condition string, args ...any) ([]domain.Message, error) {
	rows, err := r.db.Query(ctx, `SELECT `+messageColumns+` FROM catwire.messages WHERE `+condition, args...)
	if err != nil {
		return nil, wrapError(operation, err)
	}
	messages, err := collect(rows, scanMessage)
	if err != nil {
		return nil, wrapError(operation, err)
	}
	return messages, nil
}
