package whatsapp

import (
	"errors"
	"testing"

	"go.mau.fi/whatsmeow"

	"github.com/OBrunoW/catwire/internal/application/outbound"
)

func sampleUpload() *whatsmeow.UploadResponse {
	return &whatsmeow.UploadResponse{
		URL: "https://mmg.whatsapp.net/file", DirectPath: "/v/file", MediaKey: []byte{1},
		FileEncSHA256: []byte{2}, FileSHA256: []byte{3}, FileLength: 42,
	}
}

func stringPtr(value string) *string {
	return &value
}

func TestBuildMessageTextWithoutReplyUsesConversation(t *testing.T) {
	message := buildMessage(outbound.SendCommand{Type: outbound.MessageTypeText, Text: stringPtr("hello")}, nil)

	if message.GetConversation() != "hello" || message.ExtendedTextMessage != nil {
		t.Fatalf("unexpected message: %+v", message)
	}
}

func TestBuildMessageTextReplyCarriesQuotedContext(t *testing.T) {
	command := outbound.SendCommand{
		Type: outbound.MessageTypeText,
		Text: stringPtr("answer"),
		Reply: &outbound.ReplyReference{
			WAMessageID: "WA1", SenderJID: "5511988887777@s.whatsapp.net", Text: stringPtr("question"),
		},
	}

	message := buildMessage(command, nil)

	info := message.GetExtendedTextMessage().GetContextInfo()
	if message.GetExtendedTextMessage().GetText() != "answer" || info.GetStanzaID() != "WA1" ||
		info.GetParticipant() != "5511988887777@s.whatsapp.net" || info.GetQuotedMessage().GetConversation() != "question" {
		t.Fatalf("unexpected reply context: %+v", message)
	}
}

func TestBuildMessageImageCarriesUploadAndCaption(t *testing.T) {
	command := outbound.SendCommand{
		Type: outbound.MessageTypeImage, Text: stringPtr("caption"),
		Media: &outbound.MediaPayload{MimeType: "image/png"},
	}

	image := buildMessage(command, sampleUpload()).GetImageMessage()

	if image.GetCaption() != "caption" || image.GetMimetype() != "image/png" || image.GetFileLength() != 42 || image.GetDirectPath() != "/v/file" {
		t.Fatalf("unexpected image: %+v", image)
	}
}

func TestBuildMessageVoiceNoteSetsPTTAndOpusMime(t *testing.T) {
	command := outbound.SendCommand{
		Type:  outbound.MessageTypeAudio,
		Media: &outbound.MediaPayload{MimeType: "audio/ogg", VoiceNote: true},
	}

	audio := buildMessage(command, sampleUpload()).GetAudioMessage()

	if !audio.GetPTT() || audio.GetMimetype() != voiceNoteMimeType {
		t.Fatalf("unexpected voice note: %+v", audio)
	}
}

func TestBuildMessagePlainAudioIsNotPTT(t *testing.T) {
	command := outbound.SendCommand{
		Type:  outbound.MessageTypeAudio,
		Media: &outbound.MediaPayload{MimeType: "audio/mpeg"},
	}

	audio := buildMessage(command, sampleUpload()).GetAudioMessage()

	if audio.GetPTT() || audio.GetMimetype() != "audio/mpeg" {
		t.Fatalf("unexpected audio: %+v", audio)
	}
}

func TestBuildMessageDocumentCarriesFileName(t *testing.T) {
	command := outbound.SendCommand{
		Type: outbound.MessageTypeDocument, Text: stringPtr("contract"),
		Media: &outbound.MediaPayload{MimeType: "application/pdf", FileName: "contract.pdf"},
	}

	document := buildMessage(command, sampleUpload()).GetDocumentMessage()

	if document.GetFileName() != "contract.pdf" || document.GetCaption() != "contract" || document.GetMimetype() != "application/pdf" {
		t.Fatalf("unexpected document: %+v", document)
	}
}

func TestBuildMessageVideoCarriesCaption(t *testing.T) {
	command := outbound.SendCommand{
		Type: outbound.MessageTypeVideo, Text: stringPtr("clip"),
		Media: &outbound.MediaPayload{MimeType: "video/mp4"},
	}

	video := buildMessage(command, sampleUpload()).GetVideoMessage()

	if video.GetCaption() != "clip" || video.GetMimetype() != "video/mp4" {
		t.Fatalf("unexpected video: %+v", video)
	}
}

func TestClassifySendError(t *testing.T) {
	if !errors.Is(classifySendError(whatsmeow.ErrNotConnected), outbound.ErrInstanceDisconnected) {
		t.Fatal("not connected must map to disconnected")
	}
	var permanent *outbound.PermanentError
	if !errors.As(classifySendError(whatsmeow.ErrRecipientNotOnWhatsApp), &permanent) || permanent.Code != "RECIPIENT_NOT_ON_WHATSAPP" {
		t.Fatal("recipient not on whatsapp must be permanent")
	}
	if !errors.As(classifySendError(whatsmeow.ErrBroadcastListUnsupported), &permanent) {
		t.Fatal("broadcast list must be permanent")
	}
	transient := classifySendError(whatsmeow.ErrMessageTimedOut)
	if errors.As(transient, &permanent) || errors.Is(transient, outbound.ErrInstanceDisconnected) {
		t.Fatal("timeout must stay transient")
	}
}

func TestSenderReportsDisconnectedWithoutClient(t *testing.T) {
	sender := NewSender(NewClientRegistry())

	_, err := sender.Send(t.Context(), outbound.SendCommand{ChatJID: "5511988887777@s.whatsapp.net"})
	if !errors.Is(err, outbound.ErrInstanceDisconnected) {
		t.Fatalf("expected disconnected, got %v", err)
	}
	if _, err := sender.IsOnWhatsApp(t.Context(), [16]byte{}, "5511988887777"); !errors.Is(err, outbound.ErrInstanceDisconnected) {
		t.Fatalf("expected disconnected, got %v", err)
	}
}
