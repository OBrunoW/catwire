package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"

	"github.com/OBrunoW/catwire/internal/application/operations"
)

var privacySettingTypes = map[string]types.PrivacySettingType{
	operations.PrivacyKeyGroupAdd:     types.PrivacySettingTypeGroupAdd,
	operations.PrivacyKeyLastSeen:     types.PrivacySettingTypeLastSeen,
	operations.PrivacyKeyStatus:       types.PrivacySettingTypeStatus,
	operations.PrivacyKeyProfile:      types.PrivacySettingTypeProfile,
	operations.PrivacyKeyReadReceipts: types.PrivacySettingTypeReadReceipts,
	operations.PrivacyKeyOnline:       types.PrivacySettingTypeOnline,
	operations.PrivacyKeyCallAdd:      types.PrivacySettingTypeCallAdd,
}

type Operations struct {
	registry *ClientRegistry
}

func NewOperations(registry *ClientRegistry) *Operations {
	return &Operations{registry: registry}
}

func (o *Operations) connected(instanceID uuid.UUID) (*whatsmeow.Client, error) {
	raw, found := o.registry.get(instanceID)
	if !found || !raw.IsConnected() {
		return nil, operations.ErrInstanceDisconnected
	}
	return raw, nil
}

func (o *Operations) GetProfile(ctx context.Context, instanceID uuid.UUID) (operations.ProfileData, error) {
	raw, err := o.connected(instanceID)
	if err != nil {
		return operations.ProfileData{}, err
	}
	own := raw.Store.GetJID().ToNonAD()
	if own.IsEmpty() {
		return operations.ProfileData{}, operations.ErrInstanceDisconnected
	}
	infos, err := raw.GetUserInfo(ctx, []types.JID{own})
	if err != nil {
		return operations.ProfileData{}, fmt.Errorf("whatsapp: get user info: %w", err)
	}
	pictureURL, err := ownPictureURL(ctx, raw, own)
	if err != nil {
		return operations.ProfileData{}, err
	}
	return operations.ProfileData{
		PushName:      raw.Store.PushName,
		StatusMessage: infos[own].Status,
		PictureURL:    pictureURL,
	}, nil
}

func ownPictureURL(ctx context.Context, raw *whatsmeow.Client, own types.JID) (*string, error) {
	picture, err := raw.GetProfilePictureInfo(ctx, own, nil)
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("whatsapp: get profile picture: %w", err)
	}
	if picture == nil || picture.URL == "" {
		return nil, nil
	}
	return &picture.URL, nil
}

func (o *Operations) SetStatusMessage(ctx context.Context, instanceID uuid.UUID, text string) error {
	raw, err := o.connected(instanceID)
	if err != nil {
		return err
	}
	if err := raw.SetStatusMessage(ctx, types.SetStatusInput{Text: &text}); err != nil {
		return fmt.Errorf("whatsapp: set status message: %w", err)
	}
	return nil
}

func (o *Operations) SetPushName(ctx context.Context, instanceID uuid.UUID, name string) error {
	raw, err := o.connected(instanceID)
	if err != nil {
		return err
	}
	if err := raw.SendAppState(ctx, appstate.BuildSettingPushName(name)); err != nil {
		return fmt.Errorf("whatsapp: set push name: %w", err)
	}
	return nil
}

func (o *Operations) GetPrivacy(ctx context.Context, instanceID uuid.UUID) (operations.PrivacySettings, error) {
	raw, err := o.connected(instanceID)
	if err != nil {
		return operations.PrivacySettings{}, err
	}
	settings, err := raw.TryFetchPrivacySettings(ctx, false)
	if err != nil {
		return operations.PrivacySettings{}, fmt.Errorf("whatsapp: get privacy settings: %w", err)
	}
	return translatePrivacySettings(*settings), nil
}

func (o *Operations) SetPrivacy(
	ctx context.Context,
	instanceID uuid.UUID,
	change operations.PrivacyChange,
) (operations.PrivacySettings, error) {
	raw, err := o.connected(instanceID)
	if err != nil {
		return operations.PrivacySettings{}, err
	}
	settingType, known := privacySettingTypes[change.Key]
	if !known {
		return operations.PrivacySettings{}, fmt.Errorf("whatsapp: unknown privacy setting %q", change.Key)
	}
	settings, err := raw.SetPrivacySetting(ctx, settingType, types.PrivacySetting(change.Value))
	if err != nil {
		return operations.PrivacySettings{}, fmt.Errorf("whatsapp: set privacy setting: %w", err)
	}
	return translatePrivacySettings(settings), nil
}

func translatePrivacySettings(settings types.PrivacySettings) operations.PrivacySettings {
	return operations.PrivacySettings{
		GroupAdd:     string(settings.GroupAdd),
		LastSeen:     string(settings.LastSeen),
		Status:       string(settings.Status),
		Profile:      string(settings.Profile),
		ReadReceipts: string(settings.ReadReceipts),
		Online:       string(settings.Online),
		CallAdd:      string(settings.CallAdd),
	}
}

func (o *Operations) SendPresence(ctx context.Context, instanceID uuid.UUID, chatJID, state string) error {
	raw, err := o.connected(instanceID)
	if err != nil {
		return err
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("whatsapp: parse chat jid: %w", err)
	}
	presence, media, err := translatePresence(state)
	if err != nil {
		return err
	}
	if err := raw.SendChatPresence(ctx, chat, presence, media); err != nil {
		return fmt.Errorf("whatsapp: send chat presence: %w", err)
	}
	return nil
}

func translatePresence(state string) (types.ChatPresence, types.ChatPresenceMedia, error) {
	switch state {
	case operations.PresenceComposing:
		return types.ChatPresenceComposing, types.ChatPresenceMediaText, nil
	case operations.PresenceRecording:
		return types.ChatPresenceComposing, types.ChatPresenceMediaAudio, nil
	case operations.PresencePaused:
		return types.ChatPresencePaused, types.ChatPresenceMediaText, nil
	}
	return "", "", fmt.Errorf("whatsapp: unsupported presence state %q", state)
}

func (o *Operations) MarkRead(ctx context.Context, instanceID uuid.UUID, receipt operations.ReadReceipt) error {
	raw, err := o.connected(instanceID)
	if err != nil {
		return err
	}
	chat, err := types.ParseJID(receipt.ChatJID)
	if err != nil {
		return fmt.Errorf("whatsapp: parse chat jid: %w", err)
	}
	sender, err := types.ParseJID(receipt.SenderJID)
	if err != nil {
		return fmt.Errorf("whatsapp: parse sender jid: %w", err)
	}
	if err := raw.MarkRead(ctx, messageIDs(receipt.WAMessageIDs), receipt.ReadAt, chat, sender); err != nil {
		return fmt.Errorf("whatsapp: mark read: %w", err)
	}
	return nil
}

func messageIDs(waMessageIDs []string) []types.MessageID {
	ids := make([]types.MessageID, 0, len(waMessageIDs))
	for _, id := range waMessageIDs {
		ids = append(ids, types.MessageID(id))
	}
	return ids
}

func (o *Operations) RequestHistory(ctx context.Context, instanceID uuid.UUID, request operations.HistoryRequest) (string, error) {
	raw, err := o.connected(instanceID)
	if err != nil {
		return "", err
	}
	anchor, err := historyAnchor(request)
	if err != nil {
		return "", err
	}
	response, err := raw.SendPeerMessage(ctx, raw.BuildHistorySyncRequest(anchor, request.Count))
	if err != nil {
		return "", fmt.Errorf("whatsapp: request history: %w", err)
	}
	return string(response.ID), nil
}

func historyAnchor(request operations.HistoryRequest) (*types.MessageInfo, error) {
	chat, err := types.ParseJID(request.ChatJID)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: parse chat jid: %w", err)
	}
	return &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: request.OldestFromMe, IsGroup: chat.Server == types.GroupServer},
		ID:            types.MessageID(request.OldestWAMessageID),
		Timestamp:     request.OldestTimestamp.UTC().Truncate(time.Second),
	}, nil
}

var (
	_ operations.ProfileGateway  = (*Operations)(nil)
	_ operations.PrivacyGateway  = (*Operations)(nil)
	_ operations.PresenceGateway = (*Operations)(nil)
	_ operations.ReadGateway     = (*Operations)(nil)
	_ operations.HistoryGateway  = (*Operations)(nil)
)
