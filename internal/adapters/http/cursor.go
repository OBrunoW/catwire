package http

import (
	"encoding/base64"
	"encoding/json"

	"github.com/OBrunoW/catwire/internal/domain"
)

func EncodeCursor(position any) (string, error) {
	raw, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeCursor(cursor string, position any) error {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return domain.BadRequest("invalid cursor")
	}
	if err := json.Unmarshal(raw, position); err != nil {
		return domain.BadRequest("invalid cursor")
	}
	return nil
}
