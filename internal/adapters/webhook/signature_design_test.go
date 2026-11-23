package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func designSignature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + string(body)))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type capturedDelivery struct {
	signature string
	timestamp string
	body      []byte
}

func captureDelivery(t *testing.T, secret string, body []byte, at time.Time) capturedDelivery {
	t.Helper()
	var captured capturedDelivery
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.signature = r.Header.Get("X-Catwire-Signature")
		captured.timestamp = r.Header.Get("X-Catwire-Timestamp")
		captured.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	request := requestFor(server.URL)
	request.Secret = secret
	request.Body = body
	request.Timestamp = at
	if _, err := NewSender(Options{Timeout: time.Second, AllowPrivate: true}).Send(context.Background(), request); err != nil {
		t.Fatalf("send: %v", err)
	}
	return captured
}

func TestSentSignatureVerifiesWithDesignAlgorithm(t *testing.T) {
	body := []byte(`{"event_id":"abc","event_type":"whatsapp.message.received"}`)

	captured := captureDelivery(t, "webhook-secret", body, sendMoment)

	if captured.timestamp != strconv.FormatInt(sendMoment.Unix(), 10) {
		t.Fatalf("unexpected timestamp header: %s", captured.timestamp)
	}
	if captured.signature != designSignature("webhook-secret", captured.timestamp, captured.body) {
		t.Fatalf("signature does not match the design algorithm: %s", captured.signature)
	}
}

func TestSentSignatureRejectsTamperedInputs(t *testing.T) {
	captured := captureDelivery(t, "webhook-secret", []byte(`{"amount":10}`), sendMoment)

	cases := map[string]string{
		"tampered body":      designSignature("webhook-secret", captured.timestamp, []byte(`{"amount":11}`)),
		"tampered timestamp": designSignature("webhook-secret", strconv.FormatInt(sendMoment.Unix()+1, 10), captured.body),
		"wrong secret":       designSignature("other-secret", captured.timestamp, captured.body),
	}
	for name, forged := range cases {
		if captured.signature == forged {
			t.Fatalf("%s must not produce the delivered signature", name)
		}
	}
}

func TestSentSignatureChangesWithTimestamp(t *testing.T) {
	body := []byte(`{"ok":true}`)

	first := captureDelivery(t, "webhook-secret", body, sendMoment)
	second := captureDelivery(t, "webhook-secret", body, sendMoment.Add(time.Second))

	if first.signature == second.signature {
		t.Fatal("signature must be bound to the timestamp")
	}
	if second.signature != designSignature("webhook-secret", second.timestamp, second.body) {
		t.Fatal("second signature must verify with the design algorithm")
	}
}
