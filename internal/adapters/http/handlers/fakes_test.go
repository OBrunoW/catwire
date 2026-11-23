package handlers

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/application/contacts"
	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/application/instance"
	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/application/operations"
	"github.com/OBrunoW/catwire/internal/application/outbound"
	"github.com/OBrunoW/catwire/internal/application/query"
	"github.com/OBrunoW/catwire/internal/application/session"
	"github.com/OBrunoW/catwire/internal/application/webhook"
	"github.com/OBrunoW/catwire/internal/domain"
)

const testOwner = "crm"

var (
	testInstanceID = uuid.MustParse("0193a1f2-7c1e-7b8a-9d4e-1f2a3b4c5d6e")
	testChatID     = uuid.MustParse("0193a1f3-0000-7000-8000-0000000000c1")
	testMessageID  = uuid.MustParse("0193a1f3-0000-7000-8000-000000000001")
	testMediaID    = uuid.MustParse("0193a1f3-0000-7000-8000-0000000000a1")
	testWebhookID  = uuid.MustParse("0193a1f4-0000-7000-8000-0000000000b1")
	testDeliveryID = uuid.MustParse("0193a1f5-0000-7000-8000-0000000000d1")
	testMoment     = time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC)
)

type ownership struct{}

func (ownership) check(clientID string) error {
	if clientID != testOwner {
		return domain.InstanceNotFound()
	}
	return nil
}

func emptyPage[T any]() apihttp.PageResult[T] {
	return apihttp.PageResult[T]{Data: []T{}, Limit: 50}
}

func testResource() instance.Resource {
	return instance.Resource{ID: testInstanceID, Name: "Main", Status: domain.InstanceStatusCreated, CreatedAt: testMoment, UpdatedAt: testMoment}
}

type fakeInstances struct {
	ownership
	created instance.CreateInput
	updated instance.UpdateInput
	filter  domain.InstanceFilter
	failure error
}

func (f *fakeInstances) Create(_ context.Context, _ string, input instance.CreateInput) (instance.Resource, error) {
	f.created = input
	return testResource(), f.failure
}

func (f *fakeInstances) Get(_ context.Context, clientID string, _ uuid.UUID) (instance.Resource, error) {
	return testResource(), f.check(clientID)
}

func (f *fakeInstances) List(_ context.Context, _ string, filter domain.InstanceFilter, _ apihttp.PageParams) (apihttp.PageResult[instance.Resource], error) {
	f.filter = filter
	return emptyPage[instance.Resource](), nil
}

func (f *fakeInstances) Update(_ context.Context, clientID string, _ uuid.UUID, input instance.UpdateInput) (instance.Resource, error) {
	f.updated = input
	return testResource(), f.check(clientID)
}

type fakeSessions struct {
	deleted   bool
	pairPhone string
	failure   error
}

func (f *fakeSessions) Connect(context.Context, uuid.UUID) (session.ConnectResult, error) {
	if f.failure != nil {
		return session.ConnectResult{}, f.failure
	}
	item := &domain.Instance{ID: testInstanceID, Name: "Main", Status: domain.InstanceStatusQRRequired, CreatedAt: testMoment, UpdatedAt: testMoment}
	return session.ConnectResult{Instance: item, QR: &session.QRCode{Code: "2@abc", ExpiresAt: testMoment}}, nil
}

func (f *fakeSessions) QR(context.Context, uuid.UUID) (session.QRCode, error) {
	return session.QRCode{Code: "2@abc", ExpiresAt: testMoment}, f.failure
}

func (f *fakeSessions) PairPhone(_ context.Context, _ uuid.UUID, phone string) (session.PairCode, error) {
	f.pairPhone = phone
	return session.PairCode{Code: "ABCD-EFGH", ExpiresAt: testMoment}, f.failure
}

func (f *fakeSessions) Disconnect(context.Context, uuid.UUID) (*domain.Instance, error) {
	return &domain.Instance{ID: testInstanceID, Name: "Main", Status: domain.InstanceStatusDisconnected}, f.failure
}

func (f *fakeSessions) Logout(context.Context, uuid.UUID) (*domain.Instance, error) {
	return &domain.Instance{ID: testInstanceID, Name: "Main", Status: domain.InstanceStatusLoggedOut}, f.failure
}

func (f *fakeSessions) Delete(context.Context, uuid.UUID) error {
	f.deleted = true
	return f.failure
}

type fakeSettings struct {
	ownership
	patch instance.SettingsPatch
}

func (f *fakeSettings) GetSettings(_ context.Context, clientID string, _ uuid.UUID) (instance.SettingsResource, error) {
	return instance.SettingsResource{SyncHistory: true, DownloadMedia: true}, f.check(clientID)
}

func (f *fakeSettings) UpdateSettings(_ context.Context, clientID string, _ uuid.UUID, patch instance.SettingsPatch) (instance.SettingsResource, error) {
	f.patch = patch
	return instance.SettingsResource{}, f.check(clientID)
}

type fakeContacts struct {
	ownership
	phones []string
}

func (f *fakeContacts) Check(_ context.Context, clientID string, _ uuid.UUID, phones []string) ([]contacts.CheckResult, error) {
	f.phones = phones
	return []contacts.CheckResult{{Input: phones[0], Exists: false}}, f.check(clientID)
}

type fakeSender struct {
	ownership
	key      string
	request  outbound.SendRequest
	replayed bool
	failure  error
}

func (f *fakeSender) Send(_ context.Context, clientID string, _ uuid.UUID, key string, request outbound.SendRequest) (outbound.SendResult, error) {
	f.key, f.request = key, request
	if err := f.check(clientID); err != nil {
		return outbound.SendResult{}, err
	}
	if f.failure != nil {
		return outbound.SendResult{}, f.failure
	}
	message := domain.Message{ID: testMessageID, InstanceID: testInstanceID, ChatID: testChatID, WAMessageID: "3EB0", Status: "pending", Timestamp: testMoment}
	return outbound.SendResult{MessageView: outbound.MessageView{Message: message, ChatJID: "5511988887777@s.whatsapp.net"}, Replayed: f.replayed}, nil
}

type fakeMessageQueries struct {
	ownership
	filter domain.MessageFilter
}

func (f *fakeMessageQueries) ListMessages(_ context.Context, clientID string, _ uuid.UUID, filter domain.MessageFilter, _ apihttp.PageParams) (apihttp.PageResult[ingestion.MessageResource], error) {
	f.filter = filter
	return emptyPage[ingestion.MessageResource](), f.check(clientID)
}

func (f *fakeMessageQueries) GetMessage(_ context.Context, clientID string, _, _ uuid.UUID) (ingestion.MessageResource, error) {
	return ingestion.MessageResource{ID: testMessageID}, f.check(clientID)
}

type fakeChatQueries struct {
	ownership
	filter domain.ChatFilter
}

func (f *fakeChatQueries) ListChats(_ context.Context, clientID string, _ uuid.UUID, filter domain.ChatFilter, _ apihttp.PageParams) (apihttp.PageResult[query.ChatResource], error) {
	f.filter = filter
	return emptyPage[query.ChatResource](), f.check(clientID)
}

func (f *fakeChatQueries) GetChat(_ context.Context, clientID string, _, _ uuid.UUID) (query.ChatResource, error) {
	return query.ChatResource{ID: testChatID}, f.check(clientID)
}

type fakeReads struct {
	ownership
	messageIDs []uuid.UUID
}

func (f *fakeReads) Mark(_ context.Context, clientID string, _, chatID uuid.UUID, messageIDs []uuid.UUID) (operations.ReadResult, error) {
	f.messageIDs = messageIDs
	return operations.ReadResult{ChatID: chatID, Marked: 3}, f.check(clientID)
}

type fakePresence struct {
	ownership
	state string
}

func (f *fakePresence) Send(_ context.Context, clientID string, _, _ uuid.UUID, state string) error {
	f.state = state
	return f.check(clientID)
}

type fakeHistory struct {
	ownership
	count int
}

func (f *fakeHistory) Request(_ context.Context, clientID string, _, chatID uuid.UUID, count int) (operations.HistoryResult, error) {
	f.count = count
	return operations.HistoryResult{RequestID: "3EB0REQ", ChatID: chatID}, f.check(clientID)
}

type fakeProfiles struct {
	ownership
	update operations.ProfileUpdate
}

func (f *fakeProfiles) Get(_ context.Context, clientID string, _ uuid.UUID) (operations.Profile, error) {
	return operations.Profile{PushName: "Main", StatusMessage: "hello"}, f.check(clientID)
}

func (f *fakeProfiles) Update(_ context.Context, clientID string, _ uuid.UUID, update operations.ProfileUpdate) (operations.Profile, error) {
	f.update = update
	return operations.Profile{PushName: "Main"}, f.check(clientID)
}

type fakePrivacy struct {
	ownership
	update operations.PrivacyUpdate
}

func (f *fakePrivacy) Get(_ context.Context, clientID string, _ uuid.UUID) (operations.PrivacySettings, error) {
	return operations.PrivacySettings{LastSeen: "contacts"}, f.check(clientID)
}

func (f *fakePrivacy) Update(_ context.Context, clientID string, _ uuid.UUID, update operations.PrivacyUpdate) (operations.PrivacySettings, error) {
	f.update = update
	return operations.PrivacySettings{LastSeen: "none"}, f.check(clientID)
}

type uploadCapture struct {
	content  string
	fileName string
	mimeType string
}

type fakeLibrary struct {
	uploaded uploadCapture
}

func (f *fakeLibrary) Upload(_ context.Context, clientID string, _ uuid.UUID, input media.UploadInput) (media.DetailResource, error) {
	if err := (ownership{}).check(clientID); err != nil {
		return media.DetailResource{}, err
	}
	content, err := io.ReadAll(input.Body)
	if err != nil {
		return media.DetailResource{}, err
	}
	f.uploaded = uploadCapture{content: string(content), fileName: input.FileName, mimeType: input.MimeType}
	return media.DetailResource{MediaResource: domain.MediaResource{ID: testMediaID, Status: domain.MediaStatusStored}, InstanceID: testInstanceID}, nil
}

func (f *fakeLibrary) Get(_ context.Context, clientID string, _ uuid.UUID) (media.DetailResource, error) {
	if clientID != testOwner {
		return media.DetailResource{}, domain.MediaNotFound()
	}
	return media.DetailResource{MediaResource: domain.MediaResource{ID: testMediaID}, InstanceID: testInstanceID}, nil
}

type readSeekCloser struct {
	*bytes.Reader
}

func (readSeekCloser) Close() error { return nil }

type fakeContent struct {
	media      domain.Media
	failure    error
	signature  string
	signedSeen bool
}

func (f *fakeContent) open() (*media.Content, error) {
	if f.failure != nil {
		return nil, f.failure
	}
	return &media.Content{Media: f.media, Body: readSeekCloser{bytes.NewReader([]byte("abcdef"))}}, nil
}

func (f *fakeContent) OpenSigned(_ context.Context, _ uuid.UUID, expires, signature string) (*media.Content, error) {
	f.signedSeen = true
	if signature != f.signature || expires == "" {
		return nil, domain.Unauthorized("invalid media url signature")
	}
	return f.open()
}

func (f *fakeContent) OpenForClient(_ context.Context, clientID string, _ uuid.UUID) (*media.Content, error) {
	if clientID != testOwner {
		return nil, domain.MediaNotFound()
	}
	return f.open()
}

type fakeWebhooks struct {
	created webhook.CreateInput
	updated domain.WebhookUpdate
	active  *bool
	filter  domain.DeliveryFilter
}

func (f *fakeWebhooks) scoped(clientID string) error {
	if clientID != testOwner {
		return domain.WebhookNotFound()
	}
	return nil
}

func testWebhook() webhook.Resource {
	return webhook.Resource{ID: testWebhookID, URL: "https://x.test/h", Events: []string{}, InstanceIDs: []uuid.UUID{}, Active: true}
}

func testDelivery() *domain.WebhookDelivery {
	return &domain.WebhookDelivery{ID: testDeliveryID, WebhookID: testWebhookID, EventType: "webhook.test", Status: "pending", NextAttemptAt: testMoment}
}

func (f *fakeWebhooks) Create(_ context.Context, _ string, input webhook.CreateInput) (webhook.CreatedResource, error) {
	f.created = input
	return webhook.CreatedResource{Resource: testWebhook(), Secret: "s3cret"}, nil
}

func (f *fakeWebhooks) Get(_ context.Context, clientID string, _ uuid.UUID) (webhook.Resource, error) {
	return testWebhook(), f.scoped(clientID)
}

func (f *fakeWebhooks) List(_ context.Context, _ string, active *bool, _ apihttp.PageParams) (apihttp.PageResult[webhook.Resource], error) {
	f.active = active
	return emptyPage[webhook.Resource](), nil
}

func (f *fakeWebhooks) Update(_ context.Context, clientID string, _ uuid.UUID, update domain.WebhookUpdate) (webhook.Resource, error) {
	f.updated = update
	return testWebhook(), f.scoped(clientID)
}

func (f *fakeWebhooks) Delete(_ context.Context, clientID string, _ uuid.UUID) error {
	return f.scoped(clientID)
}

func (f *fakeWebhooks) RotateSecret(_ context.Context, clientID string, id uuid.UUID) (webhook.SecretResource, error) {
	return webhook.SecretResource{ID: id, Secret: "new"}, f.scoped(clientID)
}

func (f *fakeWebhooks) SendTest(_ context.Context, clientID string, _ uuid.UUID) (uuid.UUID, error) {
	return testDeliveryID, f.scoped(clientID)
}

func (f *fakeWebhooks) ListDeliveries(_ context.Context, clientID string, _ uuid.UUID, filter domain.DeliveryFilter, _ apihttp.PageParams) (apihttp.PageResult[domain.WebhookDelivery], error) {
	f.filter = filter
	page := apihttp.PageResult[domain.WebhookDelivery]{Data: []domain.WebhookDelivery{*testDelivery()}, Limit: 50}
	return page, f.scoped(clientID)
}

func (f *fakeWebhooks) RetryDelivery(_ context.Context, clientID string, _, _ uuid.UUID) (*domain.WebhookDelivery, error) {
	return testDelivery(), f.scoped(clientID)
}
