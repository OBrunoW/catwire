package mediafetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/OBrunoW/catwire/internal/adapters/netguard"
	"github.com/OBrunoW/catwire/internal/application/outbound"
)

const (
	defaultTimeout = 30 * time.Second
	maxRedirects   = 3
)

var (
	ErrBlockedAddress = netguard.ErrBlockedAddress
	errUnexpected     = errors.New("mediafetch: unexpected response status")
)

type Options struct {
	Timeout       time.Duration
	AllowPrivate  bool
	UserAgentName string
}

type Fetcher struct {
	client    *http.Client
	userAgent string
}

func New(options Options) *Fetcher {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	userAgent := options.UserAgentName
	if userAgent == "" {
		userAgent = "catwire"
	}
	dialer := &net.Dialer{Timeout: timeout}
	if !options.AllowPrivate {
		dialer.Control = netguard.RejectPrivateAddress
	}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ResponseHeaderTimeout: timeout}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("mediafetch: too many redirects")
			}
			return nil
		},
	}
	return &Fetcher{client: client, userAgent: userAgent}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (outbound.FetchedFile, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return outbound.FetchedFile{}, errors.New("mediafetch: invalid url")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return outbound.FetchedFile{}, errors.New("mediafetch: invalid url")
	}
	request.Header.Set("User-Agent", f.userAgent)
	response, err := f.client.Do(request)
	if err != nil {
		return outbound.FetchedFile{}, sanitize(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return outbound.FetchedFile{}, fmt.Errorf("%w: %d", errUnexpected, response.StatusCode)
	}
	return outbound.FetchedFile{
		Body:        response.Body,
		ContentType: response.Header.Get("Content-Type"),
		FileName:    fileNameOf(parsed),
	}, nil
}

func sanitize(err error) error {
	if errors.Is(err, ErrBlockedAddress) {
		return ErrBlockedAddress
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("mediafetch: request failed")
}

func fileNameOf(parsed *url.URL) string {
	name := path.Base(parsed.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

var _ outbound.Fetcher = (*Fetcher)(nil)
