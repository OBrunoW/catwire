package contacts

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/outbound"
	"github.com/OBrunoW/catwire/internal/domain"
)

type fakeInstances struct {
	instance domain.Instance
}

func (f fakeInstances) FindByID(_ context.Context, clientID string, id uuid.UUID) (*domain.Instance, error) {
	if f.instance.ClientID != clientID || f.instance.ID != id {
		return nil, domain.InstanceNotFound()
	}
	copied := f.instance
	return &copied, nil
}

type fakeChecker struct {
	calls   []string
	missing map[string]bool
	err     error
}

func (f *fakeChecker) CheckRecipient(_ context.Context, _ uuid.UUID, phone string) (outbound.Registration, error) {
	f.calls = append(f.calls, phone)
	if f.err != nil {
		return outbound.Registration{}, f.err
	}
	if f.missing[phone] {
		return outbound.Registration{}, nil
	}
	return outbound.Registration{Exists: true, JID: phone + "@s.whatsapp.net"}, nil
}

func newService(status string) (*Service, *fakeChecker, domain.Instance) {
	instance := domain.Instance{ID: uuid.New(), ClientID: "client-a", Status: status}
	checker := &fakeChecker{missing: map[string]bool{}}
	return NewService(fakeInstances{instance: instance}, checker), checker, instance
}

func phones(count int) []string {
	items := make([]string, 0, count)
	for index := 0; index < count; index++ {
		items = append(items, "5511988"+strconv.Itoa(100000+index))
	}
	return items
}

func assertCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Status != status || appErr.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

func TestCheckRejectsEmptyAndOversizedBatches(t *testing.T) {
	service, checker, instance := newService(domain.InstanceStatusConnected)

	_, empty := service.Check(context.Background(), "client-a", instance.ID, nil)
	_, oversized := service.Check(context.Background(), "client-a", instance.ID, phones(51))

	assertCode(t, empty, 400, "BAD_REQUEST")
	assertCode(t, oversized, 400, "BAD_REQUEST")
	if len(checker.calls) != 0 {
		t.Fatalf("no lookup expected, got %v", checker.calls)
	}
}

func TestCheckAcceptsBatchBoundaries(t *testing.T) {
	service, _, instance := newService(domain.InstanceStatusConnected)

	for _, size := range []int{1, 50} {
		results, err := service.Check(context.Background(), "client-a", instance.ID, phones(size))
		if err != nil || len(results) != size {
			t.Fatalf("size %d: unexpected result %d %v", size, len(results), err)
		}
	}
}

func TestCheckReturnsExistsAndMissingInInputOrder(t *testing.T) {
	service, checker, instance := newService(domain.InstanceStatusConnected)
	checker.missing["11988887777"] = true

	results, err := service.Check(context.Background(), "client-a", instance.ID, []string{"5511988887777", "11988887777"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !results[0].Exists || results[0].Phone == nil || *results[0].Phone != "5511988887777" ||
		results[0].JID == nil || *results[0].JID != "5511988887777@s.whatsapp.net" {
		t.Fatalf("unexpected first result %+v", results[0])
	}
	if results[1].Exists || results[1].JID != nil || results[1].Phone != nil || results[1].Input != "11988887777" {
		t.Fatalf("unexpected second result %+v", results[1])
	}
}

func TestCheckRequiresConnectedInstance(t *testing.T) {
	service, checker, instance := newService(domain.InstanceStatusDisconnected)

	_, err := service.Check(context.Background(), "client-a", instance.ID, phones(1))

	assertCode(t, err, 409, "INSTANCE_NOT_CONNECTED")
	if len(checker.calls) != 0 {
		t.Fatal("gateway must not be called")
	}
}

func TestCheckOfAnotherClientIsNotFound(t *testing.T) {
	service, _, instance := newService(domain.InstanceStatusConnected)

	_, err := service.Check(context.Background(), "client-b", instance.ID, phones(1))

	assertCode(t, err, 404, "INSTANCE_NOT_FOUND")
}

func TestCheckPropagatesCheckerError(t *testing.T) {
	service, checker, instance := newService(domain.InstanceStatusConnected)
	checker.err = domain.WhatsAppError("could not check")

	_, err := service.Check(context.Background(), "client-a", instance.ID, phones(2))

	assertCode(t, err, 502, "WHATSAPP_ERROR")
	if len(checker.calls) != 1 {
		t.Fatalf("expected to stop at first failure, got %d calls", len(checker.calls))
	}
}

func TestCheckRejectsWholeListWhenAnyPhoneIsInvalid(t *testing.T) {
	service, checker, instance := newService(domain.InstanceStatusConnected)

	_, err := service.Check(context.Background(), "client-a", instance.ID, []string{"5511988887777", "5511988886666", "12ab"})

	assertCode(t, err, 400, "BAD_REQUEST")
	if len(checker.calls) != 0 {
		t.Fatalf("gateway must not be called, got %v", checker.calls)
	}
}

func TestCheckValidatesPhoneLengthBoundaries(t *testing.T) {
	service, checker, instance := newService(domain.InstanceStatusConnected)

	for _, phone := range []string{"1234567", "1234567890123456"} {
		_, err := service.Check(context.Background(), "client-a", instance.ID, []string{phone})
		assertCode(t, err, 400, "BAD_REQUEST")
	}
	for _, phone := range []string{"12345678", "123456789012345"} {
		if _, err := service.Check(context.Background(), "client-a", instance.ID, []string{phone}); err != nil {
			t.Fatalf("phone %s: unexpected error %v", phone, err)
		}
	}
	if len(checker.calls) != 2 {
		t.Fatalf("expected 2 lookups, got %v", checker.calls)
	}
}
