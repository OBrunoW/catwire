package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OBrunoW/catwire/internal/domain"
)

func contentPath(query string) string {
	path := apiPath + "/media/" + testMediaID.String() + "/content"
	if query == "" {
		return path
	}
	return path + "?" + query
}

func multipartRequest(t *testing.T, fields map[string]string, fileName, content string) *http.Request {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if fileName != "" {
		part, err := writer.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, instancePath("/media"), &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Service-Key", ownerKey)
	return request
}

func TestUploadMediaStoresFileAndReturnsCreated(t *testing.T) {
	backend := newBackend()
	request := multipartRequest(t, map[string]string{"mime_type": "application/pdf"}, "contract.pdf", "pdf-bytes")
	recorder := httptest.NewRecorder()
	backend.router().ServeHTTP(recorder, request)
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusCreated).Data)
	if data["id"] != testMediaID.String() || data["status"] != "stored" || data["instance_id"] != testInstanceID.String() {
		t.Errorf("unexpected data: %v", data)
	}
	uploaded := backend.library.uploaded
	if uploaded.content != "pdf-bytes" || uploaded.fileName != "contract.pdf" || uploaded.mimeType != "application/pdf" {
		t.Errorf("unexpected upload: %+v", uploaded)
	}
}

func TestUploadMediaWithoutFileIsBadRequest(t *testing.T) {
	recorder := httptest.NewRecorder()
	newBackend().router().ServeHTTP(recorder, multipartRequest(t, map[string]string{"mime_type": "image/png"}, "", ""))
	expectError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
}

func TestUploadMediaWithJSONBodyIsBadRequest(t *testing.T) {
	recorder := call{method: http.MethodPost, path: instancePath("/media"), body: `{}`, key: ownerKey}.send(t, newBackend().router())
	expectError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
}

func TestUploadMediaAboveLimitIsTooLarge(t *testing.T) {
	backend := newBackend()
	backend.maxMedia = 16
	recorder := httptest.NewRecorder()
	request := multipartRequest(t, nil, "big.bin", strings.Repeat("x", 2<<20))
	backend.router().ServeHTTP(recorder, request)
	expectError(t, recorder, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE")
}

func TestUploadMediaOfOtherClientIsNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := multipartRequest(t, nil, "a.pdf", "x")
	request.Header.Set("X-Service-Key", otherKey)
	newBackend().router().ServeHTTP(recorder, request)
	expectError(t, recorder, http.StatusNotFound, "INSTANCE_NOT_FOUND")
}

func TestGetMediaReturnsDetail(t *testing.T) {
	recorder := call{method: http.MethodGet, path: apiPath + "/media/" + testMediaID.String(), key: ownerKey}.send(t, newBackend().router())
	data := unmarshalData(t, expectSuccess(t, recorder, http.StatusOK).Data)
	if data["id"] != testMediaID.String() || data["instance_id"] != testInstanceID.String() {
		t.Errorf("unexpected data: %v", data)
	}
	other := call{method: http.MethodGet, path: apiPath + "/media/" + testMediaID.String(), key: otherKey}.send(t, newBackend().router())
	expectError(t, other, http.StatusNotFound, "MEDIA_NOT_FOUND")
}

func TestContentWithHeaderServesBinaryWithHeaders(t *testing.T) {
	recorder := call{method: http.MethodGet, path: contentPath(""), key: ownerKey}.send(t, newBackend().router())
	if recorder.Code != http.StatusOK || recorder.Body.String() != "abcdef" {
		t.Fatalf("status %d body %q", recorder.Code, recorder.Body.String())
	}
	headers := recorder.Header()
	if headers.Get("Content-Type") != "application/pdf" || headers.Get("Content-Length") != "6" {
		t.Errorf("unexpected headers: %v", headers)
	}
	if headers.Get("Content-Disposition") != `attachment; filename=contract.pdf` {
		t.Errorf("disposition: got %q", headers.Get("Content-Disposition"))
	}
	if headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("Accept-Ranges") != "bytes" {
		t.Errorf("unexpected hardening headers: %v", headers)
	}
}

func TestContentSupportsRange(t *testing.T) {
	recorder := call{method: http.MethodGet, path: contentPath(""), key: ownerKey, headers: map[string]string{"Range": "bytes=1-2"}}.send(t, newBackend().router())
	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "bc" {
		t.Fatalf("status %d body %q", recorder.Code, recorder.Body.String())
	}
}

func TestContentWithoutDispositionForNonDocuments(t *testing.T) {
	backend := newBackend()
	imageType := "image/jpeg"
	backend.content.media = domain.Media{Kind: "image", MimeType: &imageType}
	recorder := call{method: http.MethodGet, path: contentPath(""), key: ownerKey}.send(t, backend.router())
	if recorder.Header().Get("Content-Disposition") != "" || recorder.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("unexpected headers: %v", recorder.Header())
	}
}

func TestContentWithValidSignatureNeedsNoHeader(t *testing.T) {
	backend := newBackend()
	query := url.Values{"expires": {"9999999999"}, "signature": {"good"}}.Encode()
	recorder := call{method: http.MethodGet, path: contentPath(query)}.send(t, backend.router())
	if recorder.Code != http.StatusOK || recorder.Body.String() != "abcdef" || !backend.content.signedSeen {
		t.Fatalf("status %d body %q", recorder.Code, recorder.Body.String())
	}
}

func TestContentWithInvalidSignatureIsUnauthorizedEvenWithHeader(t *testing.T) {
	query := url.Values{"expires": {"9999999999"}, "signature": {"bad"}}.Encode()
	recorder := call{method: http.MethodGet, path: contentPath(query), key: ownerKey}.send(t, newBackend().router())
	expectError(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestContentSignedAccessDisabledIsUnauthorized(t *testing.T) {
	backend := newBackend()
	backend.signed = false
	query := url.Values{"expires": {"9999999999"}, "signature": {"good"}}.Encode()
	recorder := call{method: http.MethodGet, path: contentPath(query)}.send(t, backend.router())
	expectError(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
	if backend.content.signedSeen {
		t.Error("signature must not be verified when signed access is disabled")
	}
}

func TestContentNotReadyIsJSONConflict(t *testing.T) {
	backend := newBackend()
	backend.content.failure = domain.MediaNotReady()
	recorder := call{method: http.MethodGet, path: contentPath(""), key: ownerKey}.send(t, backend.router())
	expectError(t, recorder, http.StatusConflict, "MEDIA_NOT_READY")
}

func TestContentOfOtherClientIsNotFound(t *testing.T) {
	recorder := call{method: http.MethodGet, path: contentPath(""), key: otherKey}.send(t, newBackend().router())
	expectError(t, recorder, http.StatusNotFound, "MEDIA_NOT_FOUND")
}

func TestContentForcesAttachmentForUnsafeTypes(t *testing.T) {
	tests := []struct {
		mimeType    string
		contentType string
		disposition string
	}{
		{"image/svg+xml", "application/octet-stream", "attachment"},
		{"text/html", "application/octet-stream", "attachment"},
		{"image/png", "image/png", ""},
	}
	for _, test := range tests {
		t.Run(test.mimeType, func(t *testing.T) {
			backend := newBackend()
			mimeType := test.mimeType
			backend.content.media = domain.Media{Kind: "image", MimeType: &mimeType}
			recorder := call{method: http.MethodGet, path: contentPath(""), key: ownerKey}.send(t, backend.router())
			if recorder.Header().Get("Content-Type") != test.contentType {
				t.Errorf("content type: got %q", recorder.Header().Get("Content-Type"))
			}
			if recorder.Header().Get("Content-Disposition") != test.disposition {
				t.Errorf("disposition: got %q", recorder.Header().Get("Content-Disposition"))
			}
		})
	}
}
