package instance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const clientA = "client-a"

func newService(db *fakeDB) *Service {
	return NewService(Options{Unit: db, Instances: db, Settings: fakeSettings{db: db}})
}

func stringPointer(value string) *string { return &value }

func boolPointer(value bool) *bool { return &value }

func assertAppError(t *testing.T, err error, status int, code string) {
	t.Helper()
	appErr, ok := domain.AsAppError(err)
	if !ok || appErr.Status != status || appErr.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

func TestCreateAppliesDefaultsAndStatusCreated(t *testing.T) {
	db := newFakeDB()

	created, err := newService(db).Create(context.Background(), clientA, CreateInput{
		ExternalID: stringPointer("crm-42"),
		Name:       "  Sales  ",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Status != domain.InstanceStatusCreated || created.Name != "Sales" {
		t.Fatalf("unexpected instance %+v", created)
	}
	expected := SettingsResource{SyncHistory: true, DownloadMedia: true}
	if created.Settings != expected {
		t.Fatalf("expected default settings %+v, got %+v", expected, created.Settings)
	}
	if db.instances[created.ID].AutoConnect {
		t.Fatal("create must not enable auto connect")
	}
}

func TestCreateAppliesPartialSettings(t *testing.T) {
	db := newFakeDB()
	var patch SettingsPatch
	if err := json.Unmarshal([]byte(`{"sync_history":false,"ignore_groups":true}`), &patch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	created, err := newService(db).Create(context.Background(), clientA, CreateInput{Name: "Sales", Settings: patch})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := SettingsResource{SyncHistory: false, IgnoreGroups: true, DownloadMedia: true}
	if created.Settings != expected {
		t.Fatalf("expected %+v, got %+v", expected, created.Settings)
	}
}

func TestCreateRejectsDuplicateExternalIDInSameClient(t *testing.T) {
	db := newFakeDB()
	service := newService(db)
	input := CreateInput{ExternalID: stringPointer("crm-42"), Name: "Sales"}
	if _, err := service.Create(context.Background(), clientA, input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := service.Create(context.Background(), clientA, input)

	assertAppError(t, err, 409, "INSTANCE_ALREADY_EXISTS")
	if _, err := service.Create(context.Background(), "client-b", input); err != nil {
		t.Fatalf("another client must reuse the external id: %v", err)
	}
}

func TestCreateValidatesNameAndExternalID(t *testing.T) {
	service := newService(newFakeDB())
	cases := map[string]CreateInput{
		"empty name":        {Name: "   "},
		"long name":         {Name: strings.Repeat("a", 121)},
		"empty external id": {Name: "ok", ExternalID: stringPointer("")},
		"long external id":  {Name: "ok", ExternalID: stringPointer(strings.Repeat("a", 201))},
	}
	for label, input := range cases {
		_, err := service.Create(context.Background(), clientA, input)
		if appErr, ok := domain.AsAppError(err); !ok || appErr.Status != 400 {
			t.Fatalf("%s: expected 400, got %v", label, err)
		}
	}
}

func TestUpdateChangesOnlyProvidedFields(t *testing.T) {
	db := newFakeDB()
	service := newService(db)
	created, _ := service.Create(context.Background(), clientA, CreateInput{ExternalID: stringPointer("crm-42"), Name: "Sales"})

	renamed, err := service.Update(context.Background(), clientA, created.ID, UpdateInput{Name: stringPointer("Support")})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if renamed.Name != "Support" || renamed.ExternalID == nil || *renamed.ExternalID != "crm-42" {
		t.Fatalf("unexpected instance %+v", renamed)
	}
	relinked, err := service.Update(context.Background(), clientA, created.ID, UpdateInput{ExternalID: stringPointer("crm-43")})
	if err != nil || relinked.Name != "Support" || *relinked.ExternalID != "crm-43" {
		t.Fatalf("unexpected result %+v %v", relinked, err)
	}
}

func TestUpdateRejectsDuplicateExternalID(t *testing.T) {
	service := newService(newFakeDB())
	_, _ = service.Create(context.Background(), clientA, CreateInput{ExternalID: stringPointer("taken"), Name: "One"})
	other, _ := service.Create(context.Background(), clientA, CreateInput{ExternalID: stringPointer("free"), Name: "Two"})

	_, err := service.Update(context.Background(), clientA, other.ID, UpdateInput{ExternalID: stringPointer("taken")})

	assertAppError(t, err, 409, "INSTANCE_ALREADY_EXISTS")
}

func TestUpdateRejectsInvalidName(t *testing.T) {
	service := newService(newFakeDB())
	created, _ := service.Create(context.Background(), clientA, CreateInput{Name: "Sales"})

	_, err := service.Update(context.Background(), clientA, created.ID, UpdateInput{Name: stringPointer("")})

	assertAppError(t, err, 400, "BAD_REQUEST")
}

func TestGetAndUpdateFromAnotherClientAreNotFound(t *testing.T) {
	service := newService(newFakeDB())
	created, _ := service.Create(context.Background(), clientA, CreateInput{Name: "Sales"})

	_, getErr := service.Get(context.Background(), "client-b", created.ID)
	_, updateErr := service.Update(context.Background(), "client-b", created.ID, UpdateInput{Name: stringPointer("x")})

	assertAppError(t, getErr, 404, "INSTANCE_NOT_FOUND")
	assertAppError(t, updateErr, 404, "INSTANCE_NOT_FOUND")
}

func TestListFiltersByStatusAndPaginates(t *testing.T) {
	db := newFakeDB()
	service := newService(db)
	for _, name := range []string{"a", "b", "c"} {
		created, _ := service.Create(context.Background(), clientA, CreateInput{Name: name})
		db.instances[created.ID].Status = domain.InstanceStatusConnected
	}
	_, _ = service.Create(context.Background(), clientA, CreateInput{Name: "d"})
	_, _ = service.Create(context.Background(), "client-b", CreateInput{Name: "foreign"})

	page, err := service.List(context.Background(), clientA, domain.InstanceFilter{Status: domain.InstanceStatusConnected}, apihttp.PageParams{Limit: 2})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Data) != 2 || !page.HasMore || page.NextCursor == nil || page.Limit != 2 {
		t.Fatalf("unexpected page %+v", page)
	}
	if page.Data[0].Settings != (SettingsResource{SyncHistory: true, DownloadMedia: true}) {
		t.Fatalf("unexpected settings %+v", page.Data[0].Settings)
	}
	if db.lastList.Status != domain.InstanceStatusConnected {
		t.Fatalf("filter not forwarded: %+v", db.lastList)
	}
}

func TestListRejectsUnknownStatus(t *testing.T) {
	_, err := newService(newFakeDB()).List(context.Background(), clientA, domain.InstanceFilter{Status: "weird"}, apihttp.PageParams{Limit: 10})

	assertAppError(t, err, 400, "BAD_REQUEST")
}

func TestUpdateSettingsAppliesPatchPartially(t *testing.T) {
	service := newService(newFakeDB())
	created, _ := service.Create(context.Background(), clientA, CreateInput{Name: "Sales"})

	updated, err := service.UpdateSettings(context.Background(), clientA, created.ID, SettingsPatch{
		DownloadMedia: boolPointer(false),
		AlwaysOnline:  boolPointer(true),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := SettingsResource{SyncHistory: true, AlwaysOnline: true}
	if updated != expected {
		t.Fatalf("expected %+v, got %+v", expected, updated)
	}
	fetched, err := service.GetSettings(context.Background(), clientA, created.ID)
	if err != nil || fetched != expected {
		t.Fatalf("settings not persisted: %+v %v", fetched, err)
	}
}

func TestSettingsPatchRejectsInvalidTypesAndUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"sync_history":"yes"}`,
		`{"ignore_groups":1}`,
		`{"unknown":true}`,
		`{"sync_history":null}`,
		`{"ignore_groups":null}`,
		`{"auto_mark_read":null}`,
		`{"always_online":null}`,
		`{"download_media":null}`,
		`[]`,
	} {
		var patch SettingsPatch
		err := json.Unmarshal([]byte(body), &patch)
		assertAppError(t, err, 400, "BAD_REQUEST")
	}
}

func TestSettingsOfAnotherClientAreNotFound(t *testing.T) {
	service := newService(newFakeDB())
	created, _ := service.Create(context.Background(), clientA, CreateInput{Name: "Sales"})

	_, getErr := service.GetSettings(context.Background(), "client-b", created.ID)
	_, updateErr := service.UpdateSettings(context.Background(), "client-b", created.ID, SettingsPatch{})
	_, missingErr := service.GetSettings(context.Background(), clientA, uuid.New())

	assertAppError(t, getErr, 404, "INSTANCE_NOT_FOUND")
	assertAppError(t, updateErr, 404, "INSTANCE_NOT_FOUND")
	assertAppError(t, missingErr, 404, "INSTANCE_NOT_FOUND")
}
