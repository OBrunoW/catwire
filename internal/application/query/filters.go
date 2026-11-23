package query

import (
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

var messageTypes = map[string]bool{
	ingestion.MessageTypeText:          true,
	ingestion.MessageTypeImage:         true,
	ingestion.MessageTypeAudio:         true,
	ingestion.MessageTypeVideo:         true,
	ingestion.MessageTypeDocument:      true,
	ingestion.MessageTypeSticker:       true,
	ingestion.MessageTypeLocation:      true,
	ingestion.MessageTypeContact:       true,
	ingestion.MessageTypeUndecryptable: true,
	ingestion.MessageTypeUnsupported:   true,
}

func validateMessageFilter(filter domain.MessageFilter) error {
	switch filter.Direction {
	case "", domain.DirectionInbound, domain.DirectionOutbound:
	default:
		return domain.BadRequest("direction must be inbound or outbound")
	}
	switch filter.Source {
	case "", ingestion.SourceLive, ingestion.SourceHistory:
	default:
		return domain.BadRequest("source must be live or history")
	}
	if filter.Type != "" && !messageTypes[filter.Type] {
		return domain.BadRequest("type is not a valid message type")
	}
	if filter.Since != nil && filter.Until != nil && filter.Since.After(*filter.Until) {
		return domain.BadRequest("since must not be after until")
	}
	return nil
}

func validateChatFilter(filter domain.ChatFilter) error {
	switch filter.Type {
	case "", domain.ChatTypeDirect, domain.ChatTypeGroup:
		return nil
	}
	return domain.BadRequest("type must be direct or group")
}
