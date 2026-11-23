package whatsapp

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/OBrunoW/catwire/internal/application/session"
)

func TestTranslateEventMapsConnectionEvents(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		kind session.EventKind
	}{
		{"connected", &events.Connected{}, session.EventConnected},
		{"disconnected", &events.Disconnected{}, session.EventDisconnected},
		{"logged out", &events.LoggedOut{}, session.EventLoggedOut},
		{"stream replaced", &events.StreamReplaced{}, session.EventStreamReplaced},
		{"temporary ban", &events.TemporaryBan{}, session.EventTemporaryBan},
		{"client outdated", &events.ClientOutdated{}, session.EventClientOutdated},
		{"banned", &events.ConnectFailure{Reason: events.ConnectFailureUnknownLogout}, session.EventBanned},
		{"connect failure", &events.ConnectFailure{Reason: events.ConnectFailureGeneric}, session.EventConnectFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, relevant := translateEvent(tc.raw)
			if !relevant || event.Kind != tc.kind {
				t.Fatalf("event = %+v relevant = %v", event, relevant)
			}
		})
	}
}

func TestTranslateEventIgnoresUnrelatedEvents(t *testing.T) {
	if _, relevant := translateEvent(&events.Message{}); relevant {
		t.Fatal("message events are not connection events")
	}
}

func TestTranslateEventPairSuccessCarriesIdentity(t *testing.T) {
	event, relevant := translateEvent(&events.PairSuccess{
		ID:       types.NewADJID("5511999999999", 0, 3),
		Platform: "android",
	})
	if !relevant || event.Kind != session.EventPaired {
		t.Fatalf("event = %+v", event)
	}
	if event.Identity.Phone == nil || *event.Identity.Phone != "5511999999999" || event.Identity.JID == nil {
		t.Fatalf("identity = %+v", event.Identity)
	}
	if event.Identity.Platform == nil || *event.Identity.Platform != "android" || event.Identity.LID != nil {
		t.Fatalf("identity = %+v", event.Identity)
	}
}

func TestTranslateQRItem(t *testing.T) {
	code := translateQRItem(whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "2@x", Timeout: time.Minute})
	if code.Kind != session.QRItemCode || code.Code != "2@x" || code.Timeout != time.Minute {
		t.Fatalf("code = %+v", code)
	}
	cases := map[session.QRItemKind]whatsmeow.QRChannelItem{
		session.QRItemSuccess:        whatsmeow.QRChannelSuccess,
		session.QRItemTimeout:        whatsmeow.QRChannelTimeout,
		session.QRItemClientOutdated: whatsmeow.QRChannelClientOutdated,
		session.QRItemFailed:         whatsmeow.QRChannelErrUnexpectedEvent,
	}
	for kind, item := range cases {
		if got := translateQRItem(item); got.Kind != kind {
			t.Fatalf("item %s kind = %d, want %d", item.Event, got.Kind, kind)
		}
	}
	failed := translateQRItem(whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventError, Error: errors.New("pair failed")})
	if failed.Kind != session.QRItemFailed || failed.Reason != "pair failed" {
		t.Fatalf("failed = %+v", failed)
	}
}
