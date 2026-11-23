package media

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

func newTestSigner(t *testing.T, secret string) *Signer {
	t.Helper()
	signer, err := NewSigner(secret, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func expiresIn(offset time.Duration) string {
	return strconv.FormatInt(fixedNow.Add(offset).Unix(), 10)
}

func requireUnauthorized(t *testing.T, err error) {
	t.Helper()
	appError, ok := domain.AsAppError(err)
	if !ok || appError.Code != "UNAUTHORIZED" {
		t.Fatalf("err = %v, want UNAUTHORIZED", err)
	}
}

func TestSignerRequiresSecret(t *testing.T) {
	if _, err := NewSigner("", nil); err != ErrSignerSecretRequired {
		t.Fatalf("err = %v", err)
	}
}

func TestSignerAcceptsValidSignature(t *testing.T) {
	signer := newTestSigner(t, "secret")
	id := uuid.New()
	expires := expiresIn(time.Minute)

	if err := signer.Verify(id, expires, signer.Sign(id, expires)); err != nil {
		t.Fatal(err)
	}
}

func TestSignerRejectsInvalidSignatures(t *testing.T) {
	signer := newTestSigner(t, "secret")
	id := uuid.New()
	expires := expiresIn(time.Minute)
	valid := signer.Sign(id, expires)
	flipped := valid[:len(valid)-1] + map[bool]string{true: "1", false: "0"}[valid[len(valid)-1] == '0']
	tests := []struct {
		name      string
		mediaID   uuid.UUID
		expires   string
		signature string
	}{
		{name: "expired", mediaID: id, expires: expiresIn(-time.Second), signature: signer.Sign(id, expiresIn(-time.Second))},
		{name: "expires at now", mediaID: id, expires: expiresIn(0), signature: signer.Sign(id, expiresIn(0))},
		{name: "tampered signature", mediaID: id, expires: expires, signature: flipped},
		{name: "truncated signature", mediaID: id, expires: expires, signature: valid[:10]},
		{name: "empty signature", mediaID: id, expires: expires, signature: ""},
		{name: "not hex", mediaID: id, expires: expires, signature: strings.Repeat("zz", 32)},
		{name: "other media", mediaID: uuid.New(), expires: expires, signature: valid},
		{name: "extended expiry", mediaID: id, expires: expiresIn(time.Hour), signature: valid},
		{name: "non numeric expiry", mediaID: id, expires: "soon", signature: valid},
		{name: "other secret", mediaID: id, expires: expires, signature: newTestSigner(t, "other").Sign(id, expires)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireUnauthorized(t, signer.Verify(test.mediaID, test.expires, test.signature))
		})
	}
}

func TestSignerSignatureIsHexHMACOfIDAndExpires(t *testing.T) {
	signer := newTestSigner(t, "secret")
	id := uuid.MustParse("0193a1f3-0000-7000-8000-000000000001")

	first := signer.Sign(id, "1795870800")
	second := signer.Sign(id, "1795870800")

	if first != second || len(first) != 64 || signer.Sign(id, "1795870801") == first {
		t.Fatalf("unexpected signature %s", first)
	}
}

func TestSignedURLCarriesVerifiableQuery(t *testing.T) {
	signer := newTestSigner(t, "secret")
	id := uuid.New()

	link, expiresAt := signer.SignedURL("https://catwire.test", id, time.Hour)

	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/api/v1/media/"+id.String()+"/content" || !expiresAt.Equal(fixedNow.Add(time.Hour)) {
		t.Fatalf("link = %s expires = %v", link, expiresAt)
	}
	query := parsed.Query()
	if err := signer.Verify(id, query.Get("expires"), query.Get("signature")); err != nil {
		t.Fatal(err)
	}
}
