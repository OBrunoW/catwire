package whatsapp

import (
	"encoding/json"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
)

type mediaMessage interface {
	GetMimetype() string
	GetFileLength() uint64
	GetDirectPath() string
	GetMediaKey() []byte
	GetFileSHA256() []byte
	GetFileEncSHA256() []byte
	GetContextInfo() *waE2E.ContextInfo
}

type locationData struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      *string `json:"name"`
	Address   *string `json:"address"`
}

type contactData struct {
	DisplayName string `json:"display_name"`
	VCard       string `json:"vcard"`
}

func translateContent(message *waE2E.Message) (ingestion.Content, error) {
	switch {
	case message.GetConversation() != "":
		return textContent(message.GetConversation(), nil), nil
	case message.GetExtendedTextMessage() != nil:
		extended := message.GetExtendedTextMessage()
		return textContent(extended.GetText(), extended.GetContextInfo()), nil
	case message.GetImageMessage() != nil:
		image := message.GetImageMessage()
		content := mediaContent(ingestion.MessageTypeImage, image, image.GetCaption())
		content.Media.Width = optionalInt(image.GetWidth())
		content.Media.Height = optionalInt(image.GetHeight())
		return content, nil
	case message.GetVideoMessage() != nil:
		video := message.GetVideoMessage()
		content := mediaContent(ingestion.MessageTypeVideo, video, video.GetCaption())
		content.Media.Width = optionalInt(video.GetWidth())
		content.Media.Height = optionalInt(video.GetHeight())
		content.Media.DurationSeconds = optionalInt(video.GetSeconds())
		return content, nil
	case message.GetAudioMessage() != nil:
		audio := message.GetAudioMessage()
		content := mediaContent(ingestion.MessageTypeAudio, audio, "")
		content.Media.DurationSeconds = optionalInt(audio.GetSeconds())
		content.Media.IsVoiceNote = audio.GetPTT()
		return content, nil
	case message.GetDocumentMessage() != nil:
		document := message.GetDocumentMessage()
		content := mediaContent(ingestion.MessageTypeDocument, document, document.GetCaption())
		content.Media.FileName = optionalString(documentName(document))
		return content, nil
	case message.GetStickerMessage() != nil:
		sticker := message.GetStickerMessage()
		content := mediaContent(ingestion.MessageTypeSticker, sticker, "")
		content.Media.Width = optionalInt(sticker.GetWidth())
		content.Media.Height = optionalInt(sticker.GetHeight())
		return content, nil
	case message.GetLocationMessage() != nil:
		return locationContent(message.GetLocationMessage())
	case message.GetContactMessage() != nil:
		return contactsContent([]*waE2E.ContactMessage{message.GetContactMessage()})
	case message.GetContactsArrayMessage() != nil:
		return contactsContent(message.GetContactsArrayMessage().GetContacts())
	}
	return ingestion.Content{Type: ingestion.MessageTypeUnsupported}, nil
}

func textContent(text string, context *waE2E.ContextInfo) ingestion.Content {
	content := ingestion.Content{Type: ingestion.MessageTypeText, Text: &text}
	applyContext(&content, context)
	return content
}

func mediaContent(kind string, message mediaMessage, caption string) ingestion.Content {
	size := int64(message.GetFileLength())
	content := ingestion.Content{
		Type: kind,
		Text: optionalString(caption),
		Media: &ingestion.MediaDescriptor{
			Kind:          kind,
			MimeType:      optionalString(message.GetMimetype()),
			DirectPath:    optionalString(message.GetDirectPath()),
			MediaKey:      message.GetMediaKey(),
			FileSHA256:    message.GetFileSHA256(),
			FileEncSHA256: message.GetFileEncSHA256(),
		},
	}
	if size > 0 {
		content.Media.SizeBytes = &size
	}
	applyContext(&content, message.GetContextInfo())
	return content
}

func applyContext(content *ingestion.Content, context *waE2E.ContextInfo) {
	if context == nil {
		return
	}
	content.ReplyToWAMessageID = optionalString(context.GetStanzaID())
	content.IsForwarded = context.GetIsForwarded()
}

func documentName(document *waE2E.DocumentMessage) string {
	if document.GetFileName() != "" {
		return document.GetFileName()
	}
	return document.GetTitle()
}

func locationContent(location *waE2E.LocationMessage) (ingestion.Content, error) {
	data, err := json.Marshal(locationData{
		Latitude:  location.GetDegreesLatitude(),
		Longitude: location.GetDegreesLongitude(),
		Name:      optionalString(location.GetName()),
		Address:   optionalString(location.GetAddress()),
	})
	if err != nil {
		return ingestion.Content{}, fmt.Errorf("whatsapp: encode location: %w", err)
	}
	content := ingestion.Content{Type: ingestion.MessageTypeLocation, Data: data}
	applyContext(&content, location.GetContextInfo())
	return content, nil
}

func contactsContent(contacts []*waE2E.ContactMessage) (ingestion.Content, error) {
	items := make([]contactData, 0, len(contacts))
	for _, contact := range contacts {
		items = append(items, contactData{DisplayName: contact.GetDisplayName(), VCard: contact.GetVcard()})
	}
	data, err := json.Marshal(items)
	if err != nil {
		return ingestion.Content{}, fmt.Errorf("whatsapp: encode contacts: %w", err)
	}
	content := ingestion.Content{Type: ingestion.MessageTypeContact, Data: data}
	if len(contacts) > 0 {
		applyContext(&content, contacts[0].GetContextInfo())
	}
	return content, nil
}

func optionalInt(value uint32) *int {
	if value == 0 {
		return nil
	}
	converted := int(value)
	return &converted
}
