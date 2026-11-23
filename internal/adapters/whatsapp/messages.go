package whatsapp

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

type Ingestor interface {
	IngestMessage(ctx context.Context, instance domain.Instance, incoming ingestion.IncomingMessage) error
	IngestEdit(ctx context.Context, instance domain.Instance, change ingestion.EditChange) error
	IngestRevocation(ctx context.Context, instance domain.Instance, change ingestion.RevocationChange) error
	IngestReaction(ctx context.Context, instance domain.Instance, change ingestion.ReactionChange) error
	IngestReceipt(ctx context.Context, instance domain.Instance, change ingestion.ReceiptChange) error
	IngestHistory(ctx context.Context, instance domain.Instance, batch ingestion.HistoryBatch) error
}

type webMessageParser interface {
	ParseWebMessage(chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error)
}

type messageHandler struct {
	ctx      context.Context
	ingestor Ingestor
	instance domain.Instance
	lids     lidResolver
	parser   webMessageParser
	logger   waLog.Logger
}

func newMessageHandler(
	ctx context.Context,
	ingestor Ingestor,
	instance domain.Instance,
	lids lidResolver,
	parser webMessageParser,
	logger waLog.Logger,
) *messageHandler {
	return &messageHandler{ctx: ctx, ingestor: ingestor, instance: instance, lids: lids, parser: parser, logger: logger}
}

func (h *messageHandler) handle(raw any) {
	var err error
	switch event := raw.(type) {
	case *events.Message:
		err = h.handleMessage(event)
	case *events.UndecryptableMessage:
		err = h.handleUndecryptable(event)
	case *events.Receipt:
		err = h.handleReceipt(event)
	case *events.HistorySync:
		err = h.handleHistory(event)
	}
	if err != nil {
		h.logger.Errorf("ingest event for instance %s failed: %v", h.instance.ID, err)
	}
}

func (h *messageHandler) resolver() *identityResolver {
	return newIdentityResolver(h.lids, h.logger)
}

type messageKind int

const (
	kindMessage messageKind = iota + 1
	kindEdit
	kindRevocation
	kindReaction
)

func classifyMessage(event *events.Message) (messageKind, bool) {
	if event.Message == nil {
		return 0, false
	}
	if protocol := event.Message.GetProtocolMessage(); protocol != nil {
		return classifyProtocol(protocol)
	}
	if event.Message.GetReactionMessage() != nil {
		return kindReaction, true
	}
	if event.IsEdit {
		return kindEdit, true
	}
	if event.Message.GetSenderKeyDistributionMessage() != nil && !hasContent(event.Message) {
		return 0, false
	}
	return kindMessage, true
}

func classifyProtocol(protocol *waE2E.ProtocolMessage) (messageKind, bool) {
	switch protocol.GetType() {
	case waE2E.ProtocolMessage_REVOKE:
		return kindRevocation, true
	case waE2E.ProtocolMessage_MESSAGE_EDIT:
		return kindEdit, true
	}
	return 0, false
}

func hasContent(message *waE2E.Message) bool {
	content, err := translateContent(message)
	return err == nil && content.Type != ingestion.MessageTypeUnsupported
}

func (h *messageHandler) handleMessage(event *events.Message) error {
	if !supportedChat(event.Info.Chat) {
		return nil
	}
	kind, relevant := classifyMessage(event)
	if !relevant {
		return nil
	}
	switch kind {
	case kindEdit:
		return h.handleEdit(event)
	case kindRevocation:
		return h.handleRevocation(event)
	case kindReaction:
		return h.handleReaction(event)
	}
	incoming, err := h.incomingMessage(event, h.resolver(), ingestion.SourceLive)
	if err != nil {
		return err
	}
	return h.ingestor.IngestMessage(h.ctx, h.instance, incoming)
}

func (h *messageHandler) incomingMessage(
	event *events.Message,
	resolver *identityResolver,
	source string,
) (ingestion.IncomingMessage, error) {
	content, err := translateContent(event.Message)
	if err != nil {
		return ingestion.IncomingMessage{}, err
	}
	content.IsEphemeral = event.IsEphemeral
	content.IsViewOnce = event.IsViewOnce
	return h.buildIncoming(event.Info, content, resolver, source), nil
}

func (h *messageHandler) buildIncoming(
	info types.MessageInfo,
	content ingestion.Content,
	resolver *identityResolver,
	source string,
) ingestion.IncomingMessage {
	var chatName *string
	if !info.IsFromMe && info.Chat.Server != types.GroupServer {
		chatName = optionalString(info.PushName)
	}
	return ingestion.IncomingMessage{
		WAMessageID: info.ID,
		Chat:        resolver.chatRef(h.ctx, info.MessageSource, chatName),
		Sender:      resolver.sender(h.ctx, info),
		FromMe:      info.IsFromMe,
		Timestamp:   info.Timestamp,
		Source:      source,
		Content:     content,
	}
}

func (h *messageHandler) handleUndecryptable(event *events.UndecryptableMessage) error {
	if !supportedChat(event.Info.Chat) || event.DecryptFailMode == events.DecryptFailHide {
		return nil
	}
	content := ingestion.Content{Type: ingestion.MessageTypeUndecryptable}
	incoming := h.buildIncoming(event.Info, content, h.resolver(), ingestion.SourceLive)
	return h.ingestor.IngestMessage(h.ctx, h.instance, incoming)
}

func (h *messageHandler) handleEdit(event *events.Message) error {
	targetID := event.Info.ID
	message := event.Message
	if protocol := event.Message.GetProtocolMessage(); protocol != nil {
		targetID = protocol.GetKey().GetID()
		message = protocol.GetEditedMessage()
	}
	content, err := translateContent(message)
	if err != nil {
		return err
	}
	return h.ingestor.IngestEdit(h.ctx, h.instance, ingestion.EditChange{
		Chat:              lightChatRef(event.Info.MessageSource),
		TargetWAMessageID: targetID,
		Content:           content,
		At:                event.Info.Timestamp,
	})
}

func (h *messageHandler) handleRevocation(event *events.Message) error {
	return h.ingestor.IngestRevocation(h.ctx, h.instance, ingestion.RevocationChange{
		Chat:              lightChatRef(event.Info.MessageSource),
		TargetWAMessageID: event.Message.GetProtocolMessage().GetKey().GetID(),
		At:                event.Info.Timestamp,
	})
}

func (h *messageHandler) handleReaction(event *events.Message) error {
	reaction := event.Message.GetReactionMessage()
	reactedAt := event.Info.Timestamp
	if reaction.GetSenderTimestampMS() > 0 {
		reactedAt = time.UnixMilli(reaction.GetSenderTimestampMS())
	}
	return h.ingestor.IngestReaction(h.ctx, h.instance, ingestion.ReactionChange{
		Chat:              lightChatRef(event.Info.MessageSource),
		TargetWAMessageID: reaction.GetKey().GetID(),
		SenderJID:         h.resolver().sender(h.ctx, event.Info).JID,
		Emoji:             reaction.GetText(),
		At:                reactedAt,
	})
}

func receiptStatus(receiptType types.ReceiptType) (string, bool) {
	switch receiptType {
	case types.ReceiptTypeDelivered:
		return domain.MessageStatusDelivered, true
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayedSelf:
		return domain.MessageStatusRead, true
	case types.ReceiptTypePlayed:
		return domain.MessageStatusPlayed, true
	}
	return "", false
}

func (h *messageHandler) handleReceipt(event *events.Receipt) error {
	status, relevant := receiptStatus(event.Type)
	if !relevant || !supportedChat(event.Chat) || len(event.MessageIDs) == 0 {
		return nil
	}
	return h.ingestor.IngestReceipt(h.ctx, h.instance, ingestion.ReceiptChange{
		Chat:         lightChatRef(event.MessageSource),
		WAMessageIDs: event.MessageIDs,
		Status:       status,
		At:           event.Timestamp,
	})
}

func (h *messageHandler) handleHistory(event *events.HistorySync) error {
	if event.Data == nil {
		return nil
	}
	resolver := h.resolver()
	batch := ingestion.HistoryBatch{
		SyncType:   strings.ToLower(event.Data.GetSyncType().String()),
		ChunkOrder: int(event.Data.GetChunkOrder()),
		Progress:   int(event.Data.GetProgress()),
	}
	for _, conversation := range event.Data.GetConversations() {
		translated, relevant := h.historyConversation(conversation, resolver)
		if relevant {
			batch.Conversations = append(batch.Conversations, translated)
		}
	}
	return h.ingestor.IngestHistory(h.ctx, h.instance, batch)
}

func (h *messageHandler) historyConversation(
	conversation *waHistorySync.Conversation,
	resolver *identityResolver,
) (ingestion.HistoryConversation, bool) {
	chatJID, err := types.ParseJID(conversation.GetID())
	if err != nil {
		h.logger.Warnf("history conversation %q skipped: %v", conversation.GetID(), err)
		return ingestion.HistoryConversation{}, false
	}
	if !supportedChat(chatJID) {
		return ingestion.HistoryConversation{}, false
	}
	source := types.MessageSource{Chat: chatJID, IsGroup: chatJID.Server == types.GroupServer}
	result := ingestion.HistoryConversation{
		Chat: resolver.chatRef(h.ctx, source, optionalString(conversation.GetName())),
	}
	for _, entry := range conversation.GetMessages() {
		incoming, ok := h.historyMessage(chatJID, entry.GetMessage(), resolver)
		if ok {
			result.Messages = append(result.Messages, incoming)
		}
	}
	return result, true
}

func (h *messageHandler) historyMessage(
	chatJID types.JID,
	webMessage *waWeb.WebMessageInfo,
	resolver *identityResolver,
) (ingestion.IncomingMessage, bool) {
	event, err := h.parser.ParseWebMessage(chatJID, webMessage)
	if err != nil {
		h.logger.Warnf("history message skipped: %v", err)
		return ingestion.IncomingMessage{}, false
	}
	if kind, relevant := classifyMessage(event); !relevant || kind != kindMessage {
		return ingestion.IncomingMessage{}, false
	}
	incoming, err := h.incomingMessage(event, resolver, ingestion.SourceHistory)
	if err != nil {
		h.logger.Warnf("history message %s skipped: %v", event.Info.ID, err)
		return ingestion.IncomingMessage{}, false
	}
	return incoming, true
}
