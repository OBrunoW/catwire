package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/OBrunoW/catwire/internal/application/outbound"
)

const voiceNoteMimeType = "audio/ogg; codecs=opus"

type Sender struct {
	registry *ClientRegistry
}

func NewSender(registry *ClientRegistry) *Sender {
	return &Sender{registry: registry}
}

func (s *Sender) connected(instanceID uuid.UUID) (*whatsmeow.Client, error) {
	raw, found := s.registry.get(instanceID)
	if !found || !raw.IsConnected() {
		return nil, outbound.ErrInstanceDisconnected
	}
	return raw, nil
}

func (s *Sender) GenerateMessageID(instanceID uuid.UUID) (string, error) {
	raw, found := s.registry.get(instanceID)
	if !found {
		return "", outbound.ErrInstanceDisconnected
	}
	return raw.GenerateMessageID(), nil
}

func (s *Sender) IsOnWhatsApp(ctx context.Context, instanceID uuid.UUID, phone string) (outbound.Registration, error) {
	raw, err := s.connected(instanceID)
	if err != nil {
		return outbound.Registration{}, err
	}
	responses, err := raw.IsOnWhatsApp(ctx, []string{"+" + phone})
	if err != nil {
		return outbound.Registration{}, classifySendError(err)
	}
	if len(responses) == 0 || !responses[0].IsIn {
		return outbound.Registration{}, nil
	}
	jid := responses[0].PhoneNumber
	if jid.IsEmpty() {
		jid = responses[0].JID
	}
	if jid.IsEmpty() {
		return outbound.Registration{}, nil
	}
	return outbound.Registration{Exists: true, JID: jid.ToNonAD().String()}, nil
}

func (s *Sender) Send(ctx context.Context, command outbound.SendCommand) (outbound.SendReceipt, error) {
	raw, err := s.connected(command.InstanceID)
	if err != nil {
		return outbound.SendReceipt{}, err
	}
	chat, err := types.ParseJID(command.ChatJID)
	if err != nil {
		return outbound.SendReceipt{}, &outbound.PermanentError{Code: outbound.CodeSendFailed, Message: "invalid chat address"}
	}
	message, err := s.buildMessage(ctx, raw, command)
	if err != nil {
		return outbound.SendReceipt{}, err
	}
	response, err := raw.SendMessage(ctx, chat, message, whatsmeow.SendRequestExtra{ID: types.MessageID(command.WAMessageID)})
	if err != nil {
		return outbound.SendReceipt{}, classifySendError(err)
	}
	return outbound.SendReceipt{Timestamp: response.Timestamp}, nil
}

func (s *Sender) buildMessage(ctx context.Context, raw *whatsmeow.Client, command outbound.SendCommand) (*waE2E.Message, error) {
	if command.Media == nil {
		return buildMessage(command, nil), nil
	}
	mediaType, known := mediaTypeForKind(command.Type)
	if !known {
		return nil, &outbound.PermanentError{Code: outbound.CodeSendFailed, Message: "unsupported message type"}
	}
	upload, err := raw.UploadReader(ctx, command.Media.Body, nil, mediaType)
	if err != nil {
		return nil, classifySendError(err)
	}
	return buildMessage(command, &upload), nil
}

func buildMessage(command outbound.SendCommand, upload *whatsmeow.UploadResponse) *waE2E.Message {
	contextInfo := replyContext(command.Reply)
	switch command.Type {
	case outbound.MessageTypeImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey,
			Mimetype: proto.String(command.Media.MimeType), FileEncSHA256: upload.FileEncSHA256,
			FileSHA256: upload.FileSHA256, FileLength: proto.Uint64(upload.FileLength),
			Caption: command.Text, ContextInfo: contextInfo,
		}}
	case outbound.MessageTypeVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey,
			Mimetype: proto.String(command.Media.MimeType), FileEncSHA256: upload.FileEncSHA256,
			FileSHA256: upload.FileSHA256, FileLength: proto.Uint64(upload.FileLength),
			Caption: command.Text, ContextInfo: contextInfo,
		}}
	case outbound.MessageTypeAudio:
		return &waE2E.Message{AudioMessage: audioMessage(command, upload, contextInfo)}
	case outbound.MessageTypeDocument:
		return &waE2E.Message{DocumentMessage: documentMessage(command, upload, contextInfo)}
	}
	return textMessage(command, contextInfo)
}

func audioMessage(command outbound.SendCommand, upload *whatsmeow.UploadResponse, contextInfo *waE2E.ContextInfo) *waE2E.AudioMessage {
	mimeType := command.Media.MimeType
	if command.Media.VoiceNote {
		mimeType = voiceNoteMimeType
	}
	return &waE2E.AudioMessage{
		URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey,
		Mimetype: proto.String(mimeType), FileEncSHA256: upload.FileEncSHA256,
		FileSHA256: upload.FileSHA256, FileLength: proto.Uint64(upload.FileLength),
		PTT: proto.Bool(command.Media.VoiceNote), ContextInfo: contextInfo,
	}
}

func documentMessage(command outbound.SendCommand, upload *whatsmeow.UploadResponse, contextInfo *waE2E.ContextInfo) *waE2E.DocumentMessage {
	document := &waE2E.DocumentMessage{
		URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey,
		Mimetype: proto.String(command.Media.MimeType), FileEncSHA256: upload.FileEncSHA256,
		FileSHA256: upload.FileSHA256, FileLength: proto.Uint64(upload.FileLength),
		Caption: command.Text, ContextInfo: contextInfo,
	}
	if command.Media.FileName != "" {
		document.FileName = proto.String(command.Media.FileName)
		document.Title = proto.String(command.Media.FileName)
	}
	return document
}

func textMessage(command outbound.SendCommand, contextInfo *waE2E.ContextInfo) *waE2E.Message {
	text := ""
	if command.Text != nil {
		text = *command.Text
	}
	if contextInfo == nil {
		return &waE2E.Message{Conversation: proto.String(text)}
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: contextInfo}}
}

func replyContext(reply *outbound.ReplyReference) *waE2E.ContextInfo {
	if reply == nil {
		return nil
	}
	info := &waE2E.ContextInfo{StanzaID: proto.String(reply.WAMessageID)}
	if reply.SenderJID != "" {
		info.Participant = proto.String(reply.SenderJID)
	}
	if reply.Text != nil {
		info.QuotedMessage = &waE2E.Message{Conversation: proto.String(*reply.Text)}
	}
	return info
}

func classifySendError(err error) error {
	switch {
	case errors.Is(err, whatsmeow.ErrNotConnected), errors.Is(err, whatsmeow.ErrNotLoggedIn):
		return outbound.ErrInstanceDisconnected
	case errors.Is(err, whatsmeow.ErrRecipientNotOnWhatsApp):
		return &outbound.PermanentError{Code: "RECIPIENT_NOT_ON_WHATSAPP", Message: "recipient has no whatsapp account"}
	case errors.Is(err, whatsmeow.ErrBroadcastListUnsupported),
		errors.Is(err, whatsmeow.ErrUnknownServer),
		errors.Is(err, whatsmeow.ErrRecipientADJID),
		errors.Is(err, whatsmeow.ErrNoPermissionInGroup),
		errors.Is(err, whatsmeow.ErrInvalidImageFormat):
		return &outbound.PermanentError{Code: outbound.CodeSendFailed, Message: "whatsapp rejected the message"}
	}
	return fmt.Errorf("whatsapp: send: %w", err)
}

var _ outbound.Gateway = (*Sender)(nil)
