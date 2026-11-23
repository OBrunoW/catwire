package whatsapp

import (
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/OBrunoW/catwire/internal/application/session"
	"github.com/OBrunoW/catwire/internal/domain"
)

func translateEvent(raw any) (session.Event, bool) {
	switch event := raw.(type) {
	case *events.Connected:
		return session.Event{Kind: session.EventConnected}, true
	case *events.Disconnected:
		return session.Event{Kind: session.EventDisconnected}, true
	case *events.PairSuccess:
		return session.Event{Kind: session.EventPaired, Identity: pairedIdentity(event)}, true
	case *events.LoggedOut:
		return session.Event{Kind: session.EventLoggedOut}, true
	case *events.StreamReplaced:
		return session.Event{Kind: session.EventStreamReplaced}, true
	case *events.TemporaryBan:
		return session.Event{Kind: session.EventTemporaryBan}, true
	case *events.ClientOutdated:
		return session.Event{Kind: session.EventClientOutdated}, true
	case *events.ConnectFailure:
		return connectFailureEvent(event), true
	}
	return session.Event{}, false
}

func connectFailureEvent(event *events.ConnectFailure) session.Event {
	if event.Reason == events.ConnectFailureUnknownLogout {
		return session.Event{Kind: session.EventBanned}
	}
	return session.Event{Kind: session.EventConnectFailure, Reason: event.Message}
}

func pairedIdentity(event *events.PairSuccess) domain.InstanceIdentity {
	jid := event.ID.String()
	phone := event.ID.User
	identity := domain.InstanceIdentity{JID: &jid, Phone: &phone}
	if !event.LID.IsEmpty() {
		lid := event.LID.String()
		identity.LID = &lid
	}
	if event.BusinessName != "" {
		identity.BusinessName = &event.BusinessName
	}
	if event.Platform != "" {
		identity.Platform = &event.Platform
	}
	return identity
}

func translateQRItem(item whatsmeow.QRChannelItem) session.QRItem {
	switch item.Event {
	case whatsmeow.QRChannelEventCode:
		return session.QRItem{Kind: session.QRItemCode, Code: item.Code, Timeout: item.Timeout}
	case whatsmeow.QRChannelSuccess.Event:
		return session.QRItem{Kind: session.QRItemSuccess}
	case whatsmeow.QRChannelTimeout.Event:
		return session.QRItem{Kind: session.QRItemTimeout}
	case whatsmeow.QRChannelClientOutdated.Event:
		return session.QRItem{Kind: session.QRItemClientOutdated}
	case whatsmeow.QRChannelEventError:
		return session.QRItem{Kind: session.QRItemFailed, Reason: item.Error.Error()}
	case whatsmeow.QRChannelErrUnexpectedEvent.Event, whatsmeow.QRChannelScannedWithoutMultidevice.Event:
		return session.QRItem{Kind: session.QRItemFailed, Reason: item.Event}
	}
	return session.QRItem{}
}
