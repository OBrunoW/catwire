package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

var ErrSignerSecretRequired = errors.New("media: url signing secret is required")

type Signer struct {
	secret []byte
	now    func() time.Time
}

func NewSigner(secret string, now func() time.Time) (*Signer, error) {
	if secret == "" {
		return nil, ErrSignerSecretRequired
	}
	if now == nil {
		now = time.Now
	}
	return &Signer{secret: []byte(secret), now: now}, nil
}

func (s *Signer) Sign(mediaID uuid.UUID, expires string) string {
	return hex.EncodeToString(s.digest(mediaID, expires))
}

func (s *Signer) digest(mediaID uuid.UUID, expires string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(mediaID.String() + "." + expires))
	return mac.Sum(nil)
}

func (s *Signer) Verify(mediaID uuid.UUID, expires, signature string) error {
	expiresAt, err := strconv.ParseInt(expires, 10, 64)
	if err != nil {
		return domain.Unauthorized("invalid media url signature")
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return domain.Unauthorized("invalid media url signature")
	}
	if !hmac.Equal(provided, s.digest(mediaID, expires)) {
		return domain.Unauthorized("invalid media url signature")
	}
	if expiresAt <= s.now().Unix() {
		return domain.Unauthorized("media url expired")
	}
	return nil
}

func (s *Signer) SignedURL(baseURL string, mediaID uuid.UUID, ttl time.Duration) (string, time.Time) {
	expiresAt := s.now().Add(ttl).UTC()
	expires := strconv.FormatInt(expiresAt.Unix(), 10)
	query := url.Values{"expires": {expires}, "signature": {s.Sign(mediaID, expires)}}
	return baseURL + "/api/v1/media/" + mediaID.String() + "/content?" + query.Encode(), expiresAt
}
