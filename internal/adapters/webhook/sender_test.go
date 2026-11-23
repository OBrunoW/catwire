package webhook

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/adapters/netguard"
	appwebhook "github.com/OBrunoW/catwire/internal/application/webhook"
)

var sendMoment = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func requestFor(url string) appwebhook.Request {
	return appwebhook.Request{
		URL: url, Secret: "top-secret", DeliveryID: uuid.New(), EventID: uuid.New(),
		EventType: "whatsapp.message.received", Body: []byte(`{"event_type":"whatsapp.message.received"}`), Timestamp: sendMoment,
	}
}

func TestSendPostsBodyWithHeadersAndVerifiableSignature(t *testing.T) {
	var received http.Header
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request := requestFor(server.URL)

	status, err := NewSender(Options{Timeout: time.Second, AllowPrivate: true}).Send(context.Background(), request)

	if err != nil || status != http.StatusNoContent {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if string(receivedBody) != string(request.Body) {
		t.Fatalf("unexpected body: %s", receivedBody)
	}
	expected := map[string]string{
		"Content-Type":          "application/json",
		"User-Agent":            "catwire-webhook/1",
		"X-Catwire-Event-Id":    request.EventID.String(),
		"X-Catwire-Event-Type":  request.EventType,
		"X-Catwire-Delivery-Id": request.DeliveryID.String(),
		"X-Catwire-Timestamp":   strconv.FormatInt(sendMoment.Unix(), 10),
	}
	for name, value := range expected {
		if received.Get(name) != value {
			t.Fatalf("header %s: expected %q, got %q", name, value, received.Get(name))
		}
	}
	signature := received.Get("X-Catwire-Signature")
	if !strings.HasPrefix(signature, "sha256=") {
		t.Fatalf("unexpected signature: %s", signature)
	}
	if signature != Sign("top-secret", received.Get("X-Catwire-Timestamp"), receivedBody) {
		t.Fatal("signature must verify with the secret, timestamp and body")
	}
	if signature == Sign("other-secret", received.Get("X-Catwire-Timestamp"), receivedBody) {
		t.Fatal("signature must depend on the secret")
	}
}

func TestSignMatchesKnownVector(t *testing.T) {
	expected := "sha256=47b6ce0fca59474308e2921c247cb2493dce6b8101d90ac05bd0c6a37d0e046e"
	if got := Sign("key", "1700000000", []byte("body")); got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}
}

func TestSendReportsNon2xxStatusWithoutError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		got, err := NewSender(Options{Timeout: time.Second, AllowPrivate: true}).Send(context.Background(), requestFor(server.URL))
		server.Close()
		if err != nil || got != status {
			t.Fatalf("status=%d err=%v", got, err)
		}
	}
}

func TestSendDoesNotFollowRedirect(t *testing.T) {
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	status, err := NewSender(Options{Timeout: time.Second, AllowPrivate: true}).Send(context.Background(), requestFor(redirector.URL))

	if err != nil || status != http.StatusFound || followed {
		t.Fatalf("status=%d followed=%v err=%v", status, followed, err)
	}
}

func TestSendTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	status, err := NewSender(Options{Timeout: 50 * time.Millisecond, AllowPrivate: true}).Send(context.Background(), requestFor(server.URL))

	if status != 0 || err == nil || err.Error() != "request timed out" {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestSendErrorDoesNotExposeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL + "/hook/token-123"
	server.Close()

	_, err := NewSender(Options{Timeout: time.Second, AllowPrivate: true}).Send(context.Background(), requestFor(address))

	if err == nil || strings.Contains(err.Error(), "token-123") {
		t.Fatalf("error must not carry the url: %v", err)
	}
}

func TestSendBlocksInternalAddressesInProduction(t *testing.T) {
	loopback := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("loopback server must not be reached")
	}))
	defer loopback.Close()
	targets := []string{
		loopback.URL, "http://127.0.0.1:1/hook", "http://169.254.169.254/latest/meta-data",
		"http://100.64.0.10/hook", "http://10.0.0.5:6379/hook", "http://[::1]:1/hook", "http://localhost:1/hook",
	}
	for _, target := range targets {
		status, err := NewSender(Options{Timeout: time.Second}).Send(context.Background(), requestFor(target))
		if status != 0 || err == nil || err.Error() != "blocked address" {
			t.Errorf("%s: status=%d err=%v", target, status, err)
		}
	}
}

func TestDescribeTransportErrorUsesFixedCategories(t *testing.T) {
	refused := &url.Error{Op: "Post", URL: "http://10.0.0.5:6379", Err: &net.OpError{
		Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 6379}, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}}
	cases := map[string]error{
		"connection refused":    refused,
		"dns resolution failed": &url.Error{Op: "Post", Err: &net.DNSError{Err: "no such host 10.0.0.5", Name: "internal.example"}},
		"tls handshake failed":  &url.Error{Op: "Post", Err: &tls.CertificateVerificationError{Err: errors.New("10.0.0.5 untrusted")}},
		"blocked address":       &url.Error{Op: "Post", Err: &net.OpError{Op: "dial", Err: netguard.ErrBlockedAddress}},
		"request timed out":     context.DeadlineExceeded,
		"request failed":        &url.Error{Op: "Post", Err: errors.New("dial tcp 10.0.0.5:6379: weird")},
	}
	for expected, input := range cases {
		got := describeTransportError(input).Error()
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
		if strings.Contains(got, "10.0.0.5") {
			t.Errorf("message leaks address: %q", got)
		}
	}
}

func TestSendRefusedConnectionHidesInternalAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	_, err := NewSender(Options{Timeout: time.Second, AllowPrivate: true}).Send(context.Background(), requestFor(address))

	if err == nil || err.Error() != "connection refused" || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("unexpected error: %v", err)
	}
}
