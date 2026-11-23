package webhook

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

type serviceFixture struct {
	service    *Service
	webhooks   *fakeWebhooks
	deliveries *fakeDeliveries
	instanceID uuid.UUID
}

func newServiceFixture(allowInsecure bool) serviceFixture {
	instanceID := uuid.New()
	webhooks := newFakeWebhooks()
	deliveries := &fakeDeliveries{}
	service := NewService(ServiceOptions{
		Webhooks:         webhooks,
		Deliveries:       deliveries,
		Instances:        &fakeInstances{owned: map[uuid.UUID]string{instanceID: "client-a"}},
		AllowInsecureURL: allowInsecure,
		Now:              func() time.Time { return fixedNow },
	})
	return serviceFixture{service: service, webhooks: webhooks, deliveries: deliveries, instanceID: instanceID}
}

func assertBadRequest(t *testing.T, err error, fragment string) {
	t.Helper()
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Status != 400 || !strings.Contains(appErr.Message, fragment) {
		t.Fatalf("expected 400 containing %q, got %v", fragment, err)
	}
}

func TestCreateGeneratesHexSecretOf32Bytes(t *testing.T) {
	fixture := newServiceFixture(false)

	created, err := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})

	if err != nil {
		t.Fatal(err)
	}
	raw, decodeErr := hex.DecodeString(created.Secret)
	if decodeErr != nil || len(raw) != 32 {
		t.Fatalf("secret must be 32 bytes in hex, got %q", created.Secret)
	}
	if fixture.webhooks.items[created.ID].Secret != created.Secret || !created.Active {
		t.Fatal("secret must be stored and webhook active by default")
	}
}

func TestCreateValidatesURL(t *testing.T) {
	tooLong := "https://consumer.example/" + strings.Repeat("a", 2048)
	cases := map[string]struct {
		url           string
		allowInsecure bool
		fragment      string
	}{
		"empty":              {url: "", fragment: "required"},
		"http in production": {url: "http://consumer.example/hook", fragment: "https"},
		"unsupported scheme": {url: "ftp://consumer.example/hook", allowInsecure: true, fragment: "https"},
		"no host":            {url: "https:///hook", fragment: "invalid"},
		"too long":           {url: tooLong, fragment: "2048"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(testCase.allowInsecure)
			_, err := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: testCase.url})
			assertBadRequest(t, err, testCase.fragment)
		})
	}
}

func TestCreateAcceptsHTTPInDevelopment(t *testing.T) {
	fixture := newServiceFixture(true)
	if _, err := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "http://localhost:9000/hook"}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRejectsEventOutsideCatalog(t *testing.T) {
	fixture := newServiceFixture(false)
	_, err := fixture.service.Create(context.Background(), "client-a", CreateInput{
		URL: "https://consumer.example/hook", Events: []string{"whatsapp.message.received", "whatsapp.unknown"},
	})
	assertBadRequest(t, err, "whatsapp.unknown")
}

func TestCreateRejectsWebhookTestAsSubscribableEvent(t *testing.T) {
	fixture := newServiceFixture(false)
	_, err := fixture.service.Create(context.Background(), "client-a", CreateInput{
		URL: "https://consumer.example/hook", Events: []string{domain.EventTypeWebhookTest},
	})
	assertBadRequest(t, err, "unknown event type")
}

func TestCreateRejectsInstanceOfAnotherClient(t *testing.T) {
	fixture := newServiceFixture(false)
	_, err := fixture.service.Create(context.Background(), "client-b", CreateInput{
		URL: "https://consumer.example/hook", InstanceIDs: []uuid.UUID{fixture.instanceID},
	})
	assertBadRequest(t, err, "unknown instance")
}

func TestCreateAcceptsOwnInstanceAndCatalogEvents(t *testing.T) {
	fixture := newServiceFixture(false)
	_, err := fixture.service.Create(context.Background(), "client-a", CreateInput{
		URL: "https://consumer.example/hook", Events: domain.EventCatalog, InstanceIDs: []uuid.UUID{fixture.instanceID},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateValidatesProvidedFields(t *testing.T) {
	fixture := newServiceFixture(false)
	created, err := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})
	if err != nil {
		t.Fatal(err)
	}
	insecure := "http://consumer.example/hook"
	_, err = fixture.service.Update(context.Background(), "client-a", created.ID, domain.WebhookUpdate{URL: &insecure})
	assertBadRequest(t, err, "https")
	events := []string{"nope"}
	_, err = fixture.service.Update(context.Background(), "client-a", created.ID, domain.WebhookUpdate{Events: &events})
	assertBadRequest(t, err, "unknown event type")
	foreign := []uuid.UUID{uuid.New()}
	_, err = fixture.service.Update(context.Background(), "client-a", created.ID, domain.WebhookUpdate{InstanceIDs: &foreign})
	assertBadRequest(t, err, "unknown instance")
}

func TestUpdateAppliesValidChange(t *testing.T) {
	fixture := newServiceFixture(false)
	created, _ := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})
	changed := "https://consumer.example/other"

	updated, err := fixture.service.Update(context.Background(), "client-a", created.ID, domain.WebhookUpdate{URL: &changed})

	if err != nil || updated.URL != changed {
		t.Fatalf("unexpected result: %+v %v", updated, err)
	}
}

func TestRotateSecretReplacesSecretAndReturnsItOnce(t *testing.T) {
	fixture := newServiceFixture(false)
	created, _ := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})

	rotated, err := fixture.service.RotateSecret(context.Background(), "client-a", created.ID)

	if err != nil {
		t.Fatal(err)
	}
	if rotated.ID != created.ID || rotated.Secret == created.Secret || len(rotated.Secret) != 64 {
		t.Fatalf("unexpected rotation: %+v", rotated)
	}
	if fixture.webhooks.items[created.ID].Secret != rotated.Secret {
		t.Fatal("rotated secret must be stored")
	}
}

func TestRotateSecretOfAnotherClientIsNotFound(t *testing.T) {
	fixture := newServiceFixture(false)
	created, _ := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})

	_, err := fixture.service.RotateSecret(context.Background(), "client-b", created.ID)

	if appErr, _ := domain.AsAppError(err); appErr == nil || appErr.Status != 404 {
		t.Fatalf("expected 404, got %v", err)
	}
}

func TestSecretNeverAppearsOutsideCreateAndRotate(t *testing.T) {
	fixture := newServiceFixture(false)
	created, _ := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})
	secret := created.Secret

	got, err := fixture.service.Get(context.Background(), "client-a", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := fixture.service.List(context.Background(), "client-a", nil, apihttp.PageParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	changed := "https://consumer.example/other"
	updated, err := fixture.service.Update(context.Background(), "client-a", created.ID, domain.WebhookUpdate{URL: &changed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SendTest(context.Background(), "client-a", created.ID); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"get": got, "list": page, "update": updated, "delivery": fixture.deliveries.inserted} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), `"secret"`) {
			t.Fatalf("%s exposes the secret: %s", name, encoded)
		}
	}
}

func TestSendTestCreatesTestDeliveryForThisWebhookOnly(t *testing.T) {
	fixture := newServiceFixture(false)
	created, _ := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})

	deliveryID, err := fixture.service.SendTest(context.Background(), "client-a", created.ID)

	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.deliveries.inserted) != 1 {
		t.Fatalf("expected one delivery, got %d", len(fixture.deliveries.inserted))
	}
	delivery := fixture.deliveries.inserted[0]
	if delivery.ID != deliveryID || delivery.WebhookID != created.ID || delivery.EventType != domain.EventTypeWebhookTest {
		t.Fatalf("unexpected delivery: %+v", delivery)
	}
	var envelope domain.EventEnvelope
	if err := json.Unmarshal(delivery.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.EventID != delivery.EventID || envelope.EventType != domain.EventTypeWebhookTest || envelope.Instance != nil {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if string(envelope.Data) != `{"webhook_id":"`+created.ID.String()+`"}` {
		t.Fatalf("unexpected data: %s", envelope.Data)
	}
}

func TestSendTestForUnknownWebhookIsNotFound(t *testing.T) {
	fixture := newServiceFixture(false)
	_, err := fixture.service.SendTest(context.Background(), "client-a", uuid.New())
	if appErr, _ := domain.AsAppError(err); appErr == nil || appErr.Status != 404 {
		t.Fatalf("expected 404, got %v", err)
	}
}

func TestDeleteRemovesWebhookFromLookups(t *testing.T) {
	fixture := newServiceFixture(false)
	created, _ := fixture.service.Create(context.Background(), "client-a", CreateInput{URL: "https://consumer.example/hook"})

	if err := fixture.service.Delete(context.Background(), "client-a", created.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Get(context.Background(), "client-a", created.ID); err == nil {
		t.Fatal("deleted webhook must not be found")
	}
}

func TestRetryDeliveryResetsToPending(t *testing.T) {
	fixture := newServiceFixture(false)
	webhookID, deliveryID := uuid.New(), uuid.New()

	delivery, err := fixture.service.RetryDelivery(context.Background(), "client-a", webhookID, deliveryID)

	if err != nil || delivery.Status != domain.DeliveryStatusPending || len(fixture.deliveries.reset) != 1 {
		t.Fatalf("unexpected retry: %+v %v", delivery, err)
	}
}
