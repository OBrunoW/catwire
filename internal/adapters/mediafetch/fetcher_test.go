package mediafetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestFetchReturnsBodyContentTypeAndFileName(t *testing.T) {
	server := newServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/pdf")
		_, _ = writer.Write([]byte("pdf-bytes"))
	})
	fetcher := New(Options{AllowPrivate: true})

	file, err := fetcher.Fetch(context.Background(), server.URL+"/docs/contract.pdf?token=secret")

	if err != nil {
		t.Fatal(err)
	}
	defer file.Body.Close()
	content, _ := io.ReadAll(file.Body)
	if string(content) != "pdf-bytes" || file.ContentType != "application/pdf" || file.FileName != "contract.pdf" {
		t.Fatalf("unexpected file: %q %q %q", content, file.ContentType, file.FileName)
	}
}

func TestFetchRejectsNonOKStatusWithoutLeakingURL(t *testing.T) {
	server := newServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})
	fetcher := New(Options{AllowPrivate: true})

	_, err := fetcher.Fetch(context.Background(), server.URL+"/missing?token=secret")

	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected sanitized error, got %v", err)
	}
}

func TestFetchBlocksPrivateAddressesByDefault(t *testing.T) {
	server := newServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("internal"))
	})
	fetcher := New(Options{})

	_, err := fetcher.Fetch(context.Background(), server.URL+"/secret?token=abc")

	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected blocked address, got %v", err)
	}
}

func TestFetchRejectsUnsupportedScheme(t *testing.T) {
	fetcher := New(Options{AllowPrivate: true})

	if _, err := fetcher.Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("expected scheme rejection")
	}
}

func TestFetchStopsRedirectLoops(t *testing.T) {
	var server *httptest.Server
	server = newServer(t, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, server.URL+"/again", http.StatusFound)
	})
	fetcher := New(Options{AllowPrivate: true})

	if _, err := fetcher.Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("expected redirect limit error")
	}
}

func TestFetchHonorsTimeout(t *testing.T) {
	server := newServer(t, func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	fetcher := New(Options{AllowPrivate: true, Timeout: 50 * time.Millisecond})

	if _, err := fetcher.Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("expected timeout")
	}
}
