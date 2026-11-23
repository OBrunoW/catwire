package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/OBrunoW/catwire/internal/adapters/netguard"
	appwebhook "github.com/OBrunoW/catwire/internal/application/webhook"
)

const (
	userAgent        = "catwire-webhook/1"
	signaturePrefix  = "sha256="
	maxDrainedBytes  = 64 * 1024
	defaultTimeout   = 10 * time.Second
	headerEventID    = "X-Catwire-Event-Id"
	headerEventType  = "X-Catwire-Event-Type"
	headerDeliveryID = "X-Catwire-Delivery-Id"
	headerTimestamp  = "X-Catwire-Timestamp"
	headerSignature  = "X-Catwire-Signature"
)

var (
	errTimeout    = errors.New("request timed out")
	errRefused    = errors.New("connection refused")
	errDNS        = errors.New("dns resolution failed")
	errTLS        = errors.New("tls handshake failed")
	errBlocked    = errors.New("blocked address")
	errConnection = errors.New("request failed")
)

type Options struct {
	Timeout      time.Duration
	AllowPrivate bool
}

type Sender struct {
	client *http.Client
}

func NewSender(options Options) *Sender {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	dialer := &net.Dialer{Timeout: timeout}
	if !options.AllowPrivate {
		dialer.Control = netguard.RejectPrivateAddress
	}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: timeout}
	return &Sender{client: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func Sign(secret string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

func (s *Sender) Send(ctx context.Context, request appwebhook.Request) (int, error) {
	httpRequest, err := newHTTPRequest(ctx, request)
	if err != nil {
		return 0, err
	}
	response, err := s.client.Do(httpRequest)
	if err != nil {
		return 0, describeTransportError(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainedBytes))
	return response.StatusCode, nil
}

func newHTTPRequest(ctx context.Context, request appwebhook.Request) (*http.Request, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		return nil, errors.New("webhook url is invalid")
	}
	timestamp := strconv.FormatInt(request.Timestamp.Unix(), 10)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", userAgent)
	httpRequest.Header.Set(headerEventID, request.EventID.String())
	httpRequest.Header.Set(headerEventType, request.EventType)
	httpRequest.Header.Set(headerDeliveryID, request.DeliveryID.String())
	httpRequest.Header.Set(headerTimestamp, timestamp)
	httpRequest.Header.Set(headerSignature, Sign(request.Secret, timestamp, request.Body))
	return httpRequest, nil
}

func describeTransportError(err error) error {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, netguard.ErrBlockedAddress):
		return errBlocked
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return errTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return errRefused
	case errors.As(err, &dnsErr):
		return errDNS
	case isTLSError(err):
		return errTLS
	}
	return errConnection
}

func isTLSError(err error) bool {
	var verification *tls.CertificateVerificationError
	var recordHeader tls.RecordHeaderError
	var alert tls.AlertError
	return errors.As(err, &verification) || errors.As(err, &recordHeader) || errors.As(err, &alert)
}
