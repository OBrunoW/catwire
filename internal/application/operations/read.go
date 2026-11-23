package operations

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	readBatchSize = 100
	maxMessageIDs = 1000
)

type ReadResult struct {
	ChatID uuid.UUID
	Marked int
}

type ReadService struct {
	instances InstanceStore
	chats     ChatStore
	messages  ReadMessageStore
	gateway   ReadGateway
	logger    *slog.Logger
	now       func() time.Time
}

func NewReadService(
	instances InstanceStore,
	chats ChatStore,
	messages ReadMessageStore,
	gateway ReadGateway,
	logger *slog.Logger,
) *ReadService {
	return &ReadService{instances: instances, chats: chats, messages: messages, gateway: gateway, logger: logger, now: time.Now}
}

func (s *ReadService) Mark(ctx context.Context, clientID string, instanceID, chatID uuid.UUID, messageIDs []uuid.UUID) (ReadResult, error) {
	instance, err := findConnected(ctx, s.instances, clientID, instanceID)
	if err != nil {
		return ReadResult{}, err
	}
	view, err := s.chats.FindByID(ctx, instance.ID, chatID)
	if err != nil {
		return ReadResult{}, err
	}
	targets, err := s.targets(ctx, instance.ID, chatID, messageIDs)
	if err != nil {
		return ReadResult{}, err
	}
	marked, err := s.markAll(ctx, instance.ID, view.Chat, targets)
	return ReadResult{ChatID: chatID, Marked: marked}, err
}

func (s *ReadService) targets(ctx context.Context, instanceID, chatID uuid.UUID, messageIDs []uuid.UUID) ([]domain.Message, error) {
	if len(messageIDs) > maxMessageIDs {
		return nil, domain.BadRequest("message_ids must have at most 1000 items")
	}
	if len(messageIDs) == 0 {
		return s.messages.ListInboundReceived(ctx, instanceID, chatID)
	}
	found, err := s.messages.FindByChatAndIDs(ctx, instanceID, chatID, messageIDs)
	if err != nil {
		return nil, err
	}
	if len(found) != len(uniqueIDs(messageIDs)) {
		return nil, domain.MessageNotFound()
	}
	for _, message := range found {
		if message.Direction != domain.DirectionInbound {
			return nil, domain.BadRequest("message_ids must reference inbound messages")
		}
	}
	return onlyReceived(found), nil
}

func onlyReceived(messages []domain.Message) []domain.Message {
	received := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		if message.Status == domain.MessageStatusReceived {
			received = append(received, message)
		}
	}
	return received
}

func (s *ReadService) markAll(ctx context.Context, instanceID uuid.UUID, chat domain.Chat, targets []domain.Message) (int, error) {
	marked := 0
	for _, group := range groupBySender(chat, targets) {
		for _, batch := range chunk(group.messages, readBatchSize) {
			advanced, err := s.markBatch(ctx, instanceID, chat, group.sender, batch)
			marked += advanced
			if err != nil {
				return marked, err
			}
		}
	}
	return marked, nil
}

func (s *ReadService) markBatch(ctx context.Context, instanceID uuid.UUID, chat domain.Chat, sender string, batch []domain.Message) (int, error) {
	readAt := s.now()
	receipt := ReadReceipt{ChatJID: chat.JID, SenderJID: sender, WAMessageIDs: waMessageIDs(batch), ReadAt: readAt}
	if err := s.gateway.MarkRead(ctx, instanceID, receipt); err != nil {
		return 0, gatewayFailure(s.logger, "mark read", instanceID, err)
	}
	advancedCount := 0
	for _, message := range batch {
		_, advanced, err := s.messages.AdvanceStatus(ctx, instanceID, message.ID, domain.MessageStatusRead, readAt)
		if err != nil {
			return advancedCount, err
		}
		if advanced {
			advancedCount++
		}
	}
	return advancedCount, nil
}

type senderGroup struct {
	sender   string
	messages []domain.Message
}

func groupBySender(chat domain.Chat, messages []domain.Message) []senderGroup {
	groups := []senderGroup{}
	index := map[string]int{}
	for _, message := range messages {
		sender := chat.JID
		if chat.Type == domain.ChatTypeGroup {
			sender = message.SenderJID
		}
		position, exists := index[sender]
		if !exists {
			position = len(groups)
			index[sender] = position
			groups = append(groups, senderGroup{sender: sender})
		}
		groups[position].messages = append(groups[position].messages, message)
	}
	return groups
}

func waMessageIDs(messages []domain.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.WAMessageID)
	}
	return ids
}

func uniqueIDs(ids []uuid.UUID) map[uuid.UUID]struct{} {
	unique := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		unique[id] = struct{}{}
	}
	return unique
}

func chunk(messages []domain.Message, size int) [][]domain.Message {
	batches := [][]domain.Message{}
	for start := 0; start < len(messages); start += size {
		batches = append(batches, messages[start:min(start+size, len(messages))])
	}
	return batches
}
