package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OBrunoW/catwire/internal/application/media"
)

func newTestLocal(t *testing.T) (*Local, string) {
	t.Helper()
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	return local, root
}

func TestLocalPutWritesUnderKeyPath(t *testing.T) {
	local, root := newTestLocal(t)
	key := "instance-1/2026/11/media-1"

	if err := local.Put(context.Background(), key, strings.NewReader("payload"), 7, "image/png"); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, "instance-1", "2026", "11", "media-1"))
	if err != nil || string(content) != "payload" {
		t.Fatalf("content = %q err = %v", content, err)
	}
}

func TestLocalPutLeavesNoTemporaryFiles(t *testing.T) {
	local, root := newTestLocal(t)

	if err := local.Put(context.Background(), "a/b/c", strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "a", "b"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "c" {
		t.Fatalf("entries = %v err = %v", entries, err)
	}
}

type failingReader struct{ sent bool }

func (r *failingReader) Read(buffer []byte) (int, error) {
	if r.sent {
		return 0, errors.New("read failed")
	}
	r.sent = true
	return copy(buffer, "partial"), nil
}

func TestLocalPutIsAtomicOnFailure(t *testing.T) {
	local, root := newTestLocal(t)
	key := "a/b/c"
	if err := local.Put(context.Background(), key, strings.NewReader("original"), 8, ""); err != nil {
		t.Fatal(err)
	}

	err := local.Put(context.Background(), key, &failingReader{}, -1, "")

	if err == nil {
		t.Fatal("expected error")
	}
	content, readErr := os.ReadFile(filepath.Join(root, "a", "b", "c"))
	if readErr != nil || string(content) != "original" {
		t.Fatalf("content = %q err = %v", content, readErr)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "a", "b"))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestLocalPutRejectsSizeMismatchWithoutCreatingObject(t *testing.T) {
	local, _ := newTestLocal(t)

	err := local.Put(context.Background(), "a/b/c", strings.NewReader("abc"), 10, "")

	if err == nil {
		t.Fatal("expected size mismatch error")
	}
	if _, statErr := local.Stat(context.Background(), "a/b/c"); !errors.Is(statErr, media.ErrObjectNotFound) {
		t.Fatalf("stat err = %v", statErr)
	}
}

func TestLocalRejectsKeysOutsideRoot(t *testing.T) {
	local, _ := newTestLocal(t)
	for _, key := range []string{"../escape", "/absolute", "a/../../escape", ""} {
		t.Run(key, func(t *testing.T) {
			if err := local.Put(context.Background(), key, strings.NewReader("x"), 1, ""); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLocalOpenStatDeleteRoundTrip(t *testing.T) {
	local, _ := newTestLocal(t)
	ctx := context.Background()
	key := "a/b/c"
	if err := local.Put(ctx, key, strings.NewReader("payload"), 7, ""); err != nil {
		t.Fatal(err)
	}

	body, err := local.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(body)
	body.Close()
	info, statErr := local.Stat(ctx, key)

	if string(content) != "payload" || statErr != nil || info.Size != 7 {
		t.Fatalf("content = %q info = %+v err = %v", content, info, statErr)
	}
	if err := local.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(ctx, key); !errors.Is(err, media.ErrObjectNotFound) {
		t.Fatalf("open after delete err = %v", err)
	}
}

func TestLocalDeleteMissingObjectIsNotAnError(t *testing.T) {
	local, _ := newTestLocal(t)

	if err := local.Delete(context.Background(), "a/b/missing"); err != nil {
		t.Fatal(err)
	}
}

func TestNewLocalRequiresDirectory(t *testing.T) {
	if _, err := NewLocal(""); err == nil {
		t.Fatal("expected error")
	}
}
