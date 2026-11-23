package ingestion

import (
	"context"

	"github.com/OBrunoW/catwire/internal/domain"
)

func (s *Service) IngestHistory(ctx context.Context, instance domain.Instance, batch HistoryBatch) error {
	settings, err := s.settings.FindByInstanceID(ctx, instance.ID)
	if err != nil {
		return err
	}
	conversations := keepConversations(batch.Conversations, settings)
	return s.runUnit(ctx, func(unit Unit) error {
		summary := HistorySyncedData{
			SyncType:   batch.SyncType,
			ChunkOrder: batch.ChunkOrder,
			Progress:   batch.Progress,
			Chats:      len(conversations),
		}
		for _, conversation := range conversations {
			total, inserted, err := s.storeConversation(ctx, unit, instance, conversation)
			if err != nil {
				return err
			}
			summary.Messages += total
			summary.InsertedMessages += inserted
		}
		return s.emit(ctx, unit, instance, EventTypeHistorySynced, instance.ID.String(), summary)
	})
}

func keepConversations(conversations []HistoryConversation, settings *domain.InstanceSettings) []HistoryConversation {
	kept := make([]HistoryConversation, 0, len(conversations))
	for _, conversation := range conversations {
		if !ignoredGroup(settings, conversation.Chat) {
			kept = append(kept, conversation)
		}
	}
	return kept
}

func (s *Service) storeConversation(
	ctx context.Context,
	unit Unit,
	instance domain.Instance,
	conversation HistoryConversation,
) (int, int, error) {
	if len(conversation.Messages) == 0 {
		return 0, 0, nil
	}
	chat, err := resolveChat(ctx, unit.Chats, instance.ID, conversation.Chat)
	if err != nil {
		return 0, 0, err
	}
	inserted := 0
	for _, incoming := range conversation.Messages {
		incoming.Source = SourceHistory
		stored, err := s.storeMessage(ctx, unit, instance, chat, incoming, domain.MediaStatusSkipped)
		if err != nil {
			return 0, 0, err
		}
		if stored.inserted {
			inserted++
		}
	}
	return len(conversation.Messages), inserted, nil
}
