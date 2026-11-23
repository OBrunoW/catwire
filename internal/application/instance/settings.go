package instance

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

type SettingsPatch struct {
	SyncHistory   *bool `json:"sync_history"`
	IgnoreGroups  *bool `json:"ignore_groups"`
	AutoMarkRead  *bool `json:"auto_mark_read"`
	AlwaysOnline  *bool `json:"always_online"`
	DownloadMedia *bool `json:"download_media"`
}

const settingsPatchMessage = "settings must be an object with boolean fields: sync_history, ignore_groups, auto_mark_read, always_online, download_media"

func hasNullField(raw []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return true
		}
	}
	return false
}

func (p *SettingsPatch) UnmarshalJSON(raw []byte) error {
	if hasNullField(raw) {
		return domain.BadRequest(settingsPatchMessage)
	}
	type plain SettingsPatch
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var decoded plain
	if err := decoder.Decode(&decoded); err != nil {
		return domain.BadRequest(settingsPatchMessage)
	}
	*p = SettingsPatch(decoded)
	return nil
}

func (p SettingsPatch) apply(settings *domain.InstanceSettings) {
	assign(&settings.SyncHistory, p.SyncHistory)
	assign(&settings.IgnoreGroups, p.IgnoreGroups)
	assign(&settings.AutoMarkRead, p.AutoMarkRead)
	assign(&settings.AlwaysOnline, p.AlwaysOnline)
	assign(&settings.DownloadMedia, p.DownloadMedia)
}

func assign(target *bool, value *bool) {
	if value != nil {
		*target = *value
	}
}

func defaultSettings(instanceID uuid.UUID) domain.InstanceSettings {
	return domain.InstanceSettings{
		InstanceID:    instanceID,
		SyncHistory:   true,
		IgnoreGroups:  false,
		AutoMarkRead:  false,
		AlwaysOnline:  false,
		DownloadMedia: true,
	}
}

func (s *Service) GetSettings(ctx context.Context, clientID string, instanceID uuid.UUID) (SettingsResource, error) {
	if _, err := s.instances.FindByID(ctx, clientID, instanceID); err != nil {
		return SettingsResource{}, err
	}
	settings, err := s.settings.FindByInstanceID(ctx, instanceID)
	if err != nil {
		return SettingsResource{}, err
	}
	return NewSettingsResource(*settings), nil
}

func (s *Service) UpdateSettings(
	ctx context.Context,
	clientID string,
	instanceID uuid.UUID,
	patch SettingsPatch,
) (SettingsResource, error) {
	var updated domain.InstanceSettings
	err := s.unit.Do(ctx, func(unit Unit) error {
		if _, err := unit.Instances.FindByID(ctx, clientID, instanceID); err != nil {
			return err
		}
		current, err := unit.Settings.FindByInstanceID(ctx, instanceID)
		if err != nil {
			return err
		}
		patch.apply(current)
		if err := unit.Settings.Update(ctx, current); err != nil {
			return err
		}
		updated = *current
		return nil
	})
	if err != nil {
		return SettingsResource{}, err
	}
	return NewSettingsResource(updated), nil
}
