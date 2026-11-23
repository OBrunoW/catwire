package whatsapp

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/OBrunoW/catwire/internal/application/operations"
)

func TestTranslatePresenceMapsEachState(t *testing.T) {
	cases := []struct {
		state    string
		presence types.ChatPresence
		media    types.ChatPresenceMedia
	}{
		{operations.PresenceComposing, types.ChatPresenceComposing, types.ChatPresenceMediaText},
		{operations.PresenceRecording, types.ChatPresenceComposing, types.ChatPresenceMediaAudio},
		{operations.PresencePaused, types.ChatPresencePaused, types.ChatPresenceMediaText},
	}
	for _, testCase := range cases {
		presence, media, err := translatePresence(testCase.state)
		if err != nil || presence != testCase.presence || media != testCase.media {
			t.Fatalf("state %s: got %q %q err=%v", testCase.state, presence, media, err)
		}
	}
}

func TestTranslatePresenceRejectsUnknownState(t *testing.T) {
	if _, _, err := translatePresence("typing"); err == nil {
		t.Fatal("expected error for unknown state")
	}
}

func TestPrivacySettingTypesCoverEveryKey(t *testing.T) {
	keys := []string{
		operations.PrivacyKeyGroupAdd, operations.PrivacyKeyLastSeen, operations.PrivacyKeyStatus,
		operations.PrivacyKeyProfile, operations.PrivacyKeyReadReceipts, operations.PrivacyKeyOnline,
		operations.PrivacyKeyCallAdd,
	}
	for _, key := range keys {
		if privacySettingTypes[key] == "" {
			t.Fatalf("missing whatsapp setting type for %s", key)
		}
	}
}

func TestTranslatePrivacySettingsCopiesEveryField(t *testing.T) {
	settings := translatePrivacySettings(types.PrivacySettings{
		GroupAdd: types.PrivacySettingContacts, LastSeen: types.PrivacySettingNone, Status: types.PrivacySettingAll,
		Profile: types.PrivacySettingContactBlacklist, ReadReceipts: types.PrivacySettingAll,
		Online: types.PrivacySettingMatchLastSeen, CallAdd: types.PrivacySettingKnown,
	})

	expected := operations.PrivacySettings{
		GroupAdd: "contacts", LastSeen: "none", Status: "all", Profile: "contact_blacklist",
		ReadReceipts: "all", Online: "match_last_seen", CallAdd: "known",
	}
	if settings != expected {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}

func TestHistoryAnchorCarriesMessageIdentityAndSeconds(t *testing.T) {
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 999, time.UTC)

	anchor, err := historyAnchor(operations.HistoryRequest{
		ChatJID: "120363000000000000@g.us", OldestWAMessageID: "WA1", OldestFromMe: true, OldestTimestamp: timestamp,
	})

	if err != nil || anchor.ID != "WA1" || !anchor.IsFromMe || !anchor.IsGroup || anchor.Chat.String() != "120363000000000000@g.us" {
		t.Fatalf("unexpected anchor: %+v err=%v", anchor, err)
	}
	if anchor.Timestamp.Nanosecond() != 0 || anchor.Timestamp.Unix() != timestamp.Unix() {
		t.Fatalf("unexpected timestamp: %v", anchor.Timestamp)
	}
}

func TestHistoryAnchorRejectsInvalidChat(t *testing.T) {
	if _, err := historyAnchor(operations.HistoryRequest{ChatJID: "a:b:c@s.whatsapp.net"}); err == nil {
		t.Fatal("expected error for invalid chat jid")
	}
}

func TestMessageIDsConvertsEveryID(t *testing.T) {
	ids := messageIDs([]string{"A", "B"})

	if len(ids) != 2 || ids[0] != "A" || ids[1] != "B" {
		t.Fatalf("unexpected ids: %v", ids)
	}
}
