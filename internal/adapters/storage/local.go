package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/OBrunoW/catwire/internal/application/media"
)

const (
	directoryMode = 0o750
	temporaryName = ".tmp-*"
)

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("storage: media directory is required")
	}
	if err := os.MkdirAll(root, directoryMode); err != nil {
		return nil, fmt.Errorf("storage: create media directory: %w", err)
	}
	return &Local{root: root}, nil
}

func (l *Local) path(key string) (string, error) {
	if !filepath.IsLocal(key) {
		return "", fmt.Errorf("storage: invalid key %q", key)
	}
	return filepath.Join(l.root, filepath.FromSlash(key)), nil
}

func (l *Local) Put(ctx context.Context, key string, reader io.Reader, size int64, _ string) error {
	target, err := l.path(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("storage: create directory: %w", err)
	}
	file, err := os.CreateTemp(directory, temporaryName)
	if err != nil {
		return fmt.Errorf("storage: create temporary file: %w", err)
	}
	if err := writeAndSeal(file, reader, size); err != nil {
		return errors.Join(err, os.Remove(file.Name()))
	}
	if err := os.Rename(file.Name(), target); err != nil {
		return errors.Join(fmt.Errorf("storage: rename temporary file: %w", err), os.Remove(file.Name()))
	}
	return nil
}

func writeAndSeal(file *os.File, reader io.Reader, size int64) error {
	written, copyErr := io.Copy(file, reader)
	if copyErr != nil {
		return errors.Join(fmt.Errorf("storage: write object: %w", copyErr), file.Close())
	}
	if size >= 0 && written != size {
		return errors.Join(fmt.Errorf("storage: wrote %d bytes, expected %d", written, size), file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("storage: sync object: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("storage: close object: %w", err)
	}
	return nil
}

func (l *Local) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	target, err := l.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, media.ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: open object: %w", err)
	}
	return file, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	target, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete object: %w", err)
	}
	return nil
}

func (l *Local) Stat(_ context.Context, key string) (media.ObjectInfo, error) {
	target, err := l.path(key)
	if err != nil {
		return media.ObjectInfo{}, err
	}
	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return media.ObjectInfo{}, media.ErrObjectNotFound
	}
	if err != nil {
		return media.ObjectInfo{}, fmt.Errorf("storage: stat object: %w", err)
	}
	return media.ObjectInfo{Size: info.Size(), ModTime: info.ModTime()}, nil
}

var _ media.Storage = (*Local)(nil)
