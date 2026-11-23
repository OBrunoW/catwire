package whatsapp

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/domain"
)

type recordingIngestor struct {
	messages    []ingestion.IncomingMessage
	edits       []ingestion.EditChange
	revocations []ingestion.RevocationChange
	reactions   []ingestion.ReactionChange
	receipts    []ingestion.ReceiptChange
	histories   []ingestion.HistoryBatch
}

func (r *recordingIngestor) IngestMessage(_ context.Context, _ domain.Instance, value ingestion.IncomingMessage) error {
	r.messages = append(r.messages, value)
	return nil
}

func (r *recordingIngestor) IngestEdit(_ context.Context, _ domain.Instance, value ingestion.EditChange) error {
	r.edits = append(r.edits, value)
	return nil
}

func (r *recordingIngestor) IngestRevocation(_ context.Context, _ domain.Instance, value ingestion.RevocationChange) error {
	r.revocations = append(r.revocations, value)
	return nil
}

func (r *recordingIngestor) IngestReaction(_ context.Context, _ domain.Instance, value ingestion.ReactionChange) error {
	r.reactions = append(r.reactions, value)
	return nil
}

func (r *recordingIngestor) IngestReceipt(_ context.Context, _ domain.Instance, value ingestion.ReceiptChange) error {
	r.receipts = append(r.receipts, value)
	return nil
}

func (r *recordingIngestor) IngestHistory(_ context.Context, _ domain.Instance, value ingestion.HistoryBatch) error {
	r.histories = append(r.histories, value)
	return nil
}

type fakeLIDs struct {
	pnByLID map[types.JID]types.JID
}

func (f fakeLIDs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	return f.pnByLID[lid], nil
}

func (f fakeLIDs) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	for lid, mapped := range f.pnByLID {
		if mapped == pn {
			return lid, nil
		}
	}
	return types.EmptyJID, nil
}

type fakeParser struct{}

func (fakeParser) ParseWebMessage(chatJID types.JID, webMsg *waWeb.WebMessageInfo) (*events.Message, error) {
	evt := &events.Message{
		RawMessage: webMsg.GetMessage(),
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chatJID, Sender: chatJID, IsFromMe: webMsg.GetKey().GetFromMe()},
			ID:            webMsg.GetKey().GetID(),
			Timestamp:     time.Unix(int64(webMsg.GetMessageTimestamp()), 0),
		},
	}
	evt.UnwrapRaw()
	return evt, nil
}

var (
	contactPN  = types.NewJID("5511988887777", types.DefaultUserServer)
	contactLID = types.NewJID("98765432109876", types.HiddenUserServer)
	groupJID   = types.NewJID("120363000000000000", types.GroupServer)
	eventTime  = time.Date(2026, 11, 22, 13, 5, 0, 0, time.UTC)
)

func newTestHandler(ingestor *recordingIngestor, lids fakeLIDs) *messageHandler {
	return newMessageHandler(context.Background(), ingestor, domain.Instance{}, lids, fakeParser{}, waLog.Noop)
}

func textEvent(source types.MessageSource, id, text string) *events.Message {
	return &events.Message{
		Info:    types.MessageInfo{MessageSource: source, ID: id, PushName: "Maria", Timestamp: eventTime},
		Message: &waE2E.Message{Conversation: proto.String(text)},
	}
}

func TestHandleMessageResolvesLIDChatToPN(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	source := types.MessageSource{Chat: contactLID, Sender: contactLID, SenderAlt: contactPN}
	handler.handle(textEvent(source, "A1", "hello"))
	if len(ingestor.messages) != 1 {
		t.Fatalf("messages = %d", len(ingestor.messages))
	}
	incoming := ingestor.messages[0]
	if incoming.Chat.JID != contactPN.String() || incoming.Chat.LID == nil || *incoming.Chat.LID != contactLID.String() {
		t.Fatalf("chat = %+v", incoming.Chat)
	}
	if incoming.Chat.Phone == nil || *incoming.Chat.Phone != "5511988887777" || incoming.Chat.Name == nil || *incoming.Chat.Name != "Maria" {
		t.Fatalf("chat = %+v", incoming.Chat)
	}
	if incoming.Source != ingestion.SourceLive || incoming.FromMe || incoming.Content.Type != ingestion.MessageTypeText {
		t.Fatalf("incoming = %+v", incoming)
	}
}

func TestHandleMessageUsesLIDStoreWhenNoAlternativeAddress(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{pnByLID: map[types.JID]types.JID{contactLID: contactPN}})
	source := types.MessageSource{Chat: contactLID, Sender: contactLID}
	handler.handle(textEvent(source, "A2", "hello"))
	if got := ingestor.messages[0].Chat.JID; got != contactPN.String() {
		t.Fatalf("chat jid = %s", got)
	}
}

func TestHandleMessageKeepsLIDChatWhenMappingUnknown(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	source := types.MessageSource{Chat: contactLID, Sender: contactLID}
	handler.handle(textEvent(source, "A3", "hello"))
	chat := ingestor.messages[0].Chat
	if chat.JID != contactLID.String() || chat.LID == nil || chat.Phone != nil {
		t.Fatalf("chat = %+v", chat)
	}
}

func TestHandleMessageFromOwnPhone(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	source := types.MessageSource{Chat: contactPN, Sender: types.NewJID("5511999999999", types.DefaultUserServer), IsFromMe: true}
	handler.handle(textEvent(source, "A4", "from phone"))
	incoming := ingestor.messages[0]
	if !incoming.FromMe || incoming.Chat.JID != contactPN.String() || incoming.Chat.Name != nil {
		t.Fatalf("incoming = %+v", incoming)
	}
}

func TestHandleMessageGroupDoesNotNameChatAfterSender(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	source := types.MessageSource{Chat: groupJID, Sender: contactPN, IsGroup: true}
	handler.handle(textEvent(source, "A5", "group hello"))
	chat := ingestor.messages[0].Chat
	if !chat.Group || chat.JID != groupJID.String() || chat.Name != nil {
		t.Fatalf("chat = %+v", chat)
	}
	if sender := ingestor.messages[0].Sender; sender.JID != contactPN.String() || sender.PushName == nil {
		t.Fatalf("sender = %+v", sender)
	}
}

func TestHandleMessageIgnoresStatusBroadcastAndNewsletters(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	for _, chat := range []types.JID{types.StatusBroadcastJID, types.NewJID("123", types.NewsletterServer)} {
		handler.handle(textEvent(types.MessageSource{Chat: chat, Sender: contactPN}, "A6", "story"))
	}
	if len(ingestor.messages) != 0 {
		t.Fatalf("messages = %d", len(ingestor.messages))
	}
}

func TestHandleUndecryptableMessage(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	source := types.MessageSource{Chat: contactPN, Sender: contactPN}
	handler.handle(&events.UndecryptableMessage{Info: types.MessageInfo{MessageSource: source, ID: "U1", Timestamp: eventTime}})
	handler.handle(&events.UndecryptableMessage{
		Info:            types.MessageInfo{MessageSource: source, ID: "U2", Timestamp: eventTime},
		DecryptFailMode: events.DecryptFailHide,
	})
	if len(ingestor.messages) != 1 || ingestor.messages[0].Content.Type != ingestion.MessageTypeUndecryptable {
		t.Fatalf("messages = %+v", ingestor.messages)
	}
}

func TestHandleRevocationEditAndReaction(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{})
	source := types.MessageSource{Chat: contactPN, Sender: contactPN}
	info := types.MessageInfo{MessageSource: source, ID: "CTRL", Timestamp: eventTime}
	key := &waCommon.MessageKey{ID: proto.String("TARGET")}
	handler.handle(&events.Message{Info: info, Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Key: key, Type: waE2E.ProtocolMessage_REVOKE.Enum(),
	}}})
	handler.handle(&events.Message{Info: info, IsEdit: true, Message: &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Key:           key,
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		EditedMessage: &waE2E.Message{Conversation: proto.String("edited")},
	}}})
	handler.handle(&events.Message{Info: info, Message: &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key: key, Text: proto.String("+"), SenderTimestampMS: proto.Int64(eventTime.UnixMilli()),
	}}})
	if len(ingestor.revocations) != 1 || ingestor.revocations[0].TargetWAMessageID != "TARGET" {
		t.Fatalf("revocations = %+v", ingestor.revocations)
	}
	if len(ingestor.edits) != 1 || ingestor.edits[0].TargetWAMessageID != "TARGET" ||
		ingestor.edits[0].Content.Text == nil || *ingestor.edits[0].Content.Text != "edited" {
		t.Fatalf("edits = %+v", ingestor.edits)
	}
	if len(ingestor.reactions) != 1 || ingestor.reactions[0].Emoji != "+" || !ingestor.reactions[0].At.Equal(eventTime) {
		t.Fatalf("reactions = %+v", ingestor.reactions)
	}
	if len(ingestor.messages) != 0 {
		t.Fatalf("control messages must not be stored as messages: %+v", ingestor.messages)
	}
}

func TestHandleReceiptMapsTypesToStatuses(t *testing.T) {
	cases := []struct {
		receiptType types.ReceiptType
		status      string
		relevant    bool
	}{
		{types.ReceiptTypeDelivered, domain.MessageStatusDelivered, true},
		{types.ReceiptTypeRead, domain.MessageStatusRead, true},
		{types.ReceiptTypePlayed, domain.MessageStatusPlayed, true},
		{types.ReceiptTypeReadSelf, domain.MessageStatusRead, true},
		{types.ReceiptTypeSender, "", false},
		{types.ReceiptTypeRetry, "", false},
		{types.ReceiptTypeServerError, "", false},
	}
	for _, tc := range cases {
		ingestor := &recordingIngestor{}
		handler := newTestHandler(ingestor, fakeLIDs{})
		handler.handle(&events.Receipt{
			MessageSource: types.MessageSource{Chat: contactPN, Sender: contactPN},
			MessageIDs:    []string{"R1", "R2"},
			Timestamp:     eventTime,
			Type:          tc.receiptType,
		})
		if tc.relevant != (len(ingestor.receipts) == 1) {
			t.Fatalf("type %q relevant = %v, receipts = %d", tc.receiptType, tc.relevant, len(ingestor.receipts))
		}
		if tc.relevant && (ingestor.receipts[0].Status != tc.status || len(ingestor.receipts[0].WAMessageIDs) != 2) {
			t.Fatalf("type %q receipt = %+v", tc.receiptType, ingestor.receipts[0])
		}
	}
}

func TestHandleHistorySyncTranslatesConversations(t *testing.T) {
	ingestor := &recordingIngestor{}
	handler := newTestHandler(ingestor, fakeLIDs{pnByLID: map[types.JID]types.JID{contactLID: contactPN}})
	syncType := waHistorySync.HistorySync_INITIAL_BOOTSTRAP
	webMessage := func(id string, fromMe bool, text string) *waHistorySync.HistorySyncMsg {
		return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
			Key:              &waCommon.MessageKey{ID: proto.String(id), FromMe: proto.Bool(fromMe)},
			MessageTimestamp: proto.Uint64(uint64(eventTime.Unix())),
			Message:          &waE2E.Message{Conversation: proto.String(text)},
		}}
	}
	handler.handle(&events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:   &syncType,
		ChunkOrder: proto.Uint32(3),
		Progress:   proto.Uint32(42),
		Conversations: []*waHistorySync.Conversation{
			{ID: proto.String(contactLID.String()), Name: proto.String("Maria"), Messages: []*waHistorySync.HistorySyncMsg{
				webMessage("H1", false, "one"), webMessage("H2", true, "two"),
			}},
			{ID: proto.String("status@broadcast")},
			{ID: proto.String(groupJID.String()), Messages: []*waHistorySync.HistorySyncMsg{webMessage("H3", false, "three")}},
		},
	}})
	if len(ingestor.histories) != 1 || len(ingestor.messages) != 0 {
		t.Fatalf("histories = %d messages = %d", len(ingestor.histories), len(ingestor.messages))
	}
	batch := ingestor.histories[0]
	if batch.SyncType != "initial_bootstrap" || batch.ChunkOrder != 3 || batch.Progress != 42 || len(batch.Conversations) != 2 {
		t.Fatalf("batch = %+v", batch)
	}
	direct := batch.Conversations[0]
	if direct.Chat.JID != contactPN.String() || direct.Chat.LID == nil || len(direct.Messages) != 2 {
		t.Fatalf("direct = %+v", direct)
	}
	if !direct.Messages[1].FromMe || direct.Messages[0].Source != ingestion.SourceHistory {
		t.Fatalf("messages = %+v", direct.Messages)
	}
	if !batch.Conversations[1].Chat.Group {
		t.Fatalf("group = %+v", batch.Conversations[1])
	}
}

func TestTranslateContentImageCarriesMediaDescriptor(t *testing.T) {
	content, err := translateContent(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:     proto.String("invoice"),
		Mimetype:    proto.String("image/jpeg"),
		FileLength:  proto.Uint64(2048),
		Width:       proto.Uint32(1280),
		Height:      proto.Uint32(960),
		DirectPath:  proto.String("/v/t62/x"),
		MediaKey:    []byte{1},
		ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("PARENT"), IsForwarded: proto.Bool(true)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	media := content.Media
	if content.Type != ingestion.MessageTypeImage || content.Text == nil || *content.Text != "invoice" || media == nil {
		t.Fatalf("content = %+v", content)
	}
	if media.Kind != "image" || *media.SizeBytes != 2048 || *media.Width != 1280 || *media.Height != 960 || media.DirectPath == nil {
		t.Fatalf("media = %+v", media)
	}
	if content.ReplyToWAMessageID == nil || *content.ReplyToWAMessageID != "PARENT" || !content.IsForwarded {
		t.Fatalf("content = %+v", content)
	}
}

func TestTranslateContentVoiceNoteLocationContactAndUnsupported(t *testing.T) {
	voice, err := translateContent(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), Seconds: proto.Uint32(7)}})
	if err != nil || voice.Type != ingestion.MessageTypeAudio || !voice.Media.IsVoiceNote || *voice.Media.DurationSeconds != 7 {
		t.Fatalf("voice = %+v err = %v", voice, err)
	}
	location, err := translateContent(&waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude: proto.Float64(-23.55), DegreesLongitude: proto.Float64(-46.63), Name: proto.String("Office"),
	}})
	if err != nil || location.Type != ingestion.MessageTypeLocation || len(location.Data) == 0 {
		t.Fatalf("location = %+v err = %v", location, err)
	}
	contact, err := translateContent(&waE2E.Message{ContactMessage: &waE2E.ContactMessage{
		DisplayName: proto.String("Joao"), Vcard: proto.String("BEGIN:VCARD"),
	}})
	if err != nil || contact.Type != ingestion.MessageTypeContact || len(contact.Data) == 0 {
		t.Fatalf("contact = %+v err = %v", contact, err)
	}
	unsupported, err := translateContent(&waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{}})
	if err != nil || unsupported.Type != ingestion.MessageTypeUnsupported {
		t.Fatalf("unsupported = %+v err = %v", unsupported, err)
	}
}
