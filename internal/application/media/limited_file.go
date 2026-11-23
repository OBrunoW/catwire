package media

import (
	"fmt"
	"os"
)

type limitedFile struct {
	*os.File
	limit   int64
	written int64
}

func newLimitedFile(file *os.File, limit int64) *limitedFile {
	return &limitedFile{File: file, limit: limit}
}

func (f *limitedFile) Write(content []byte) (int, error) {
	if err := f.reserve(f.written + int64(len(content))); err != nil {
		return 0, err
	}
	count, err := f.File.Write(content)
	f.written += int64(count)
	return count, err
}

func (f *limitedFile) WriteAt(content []byte, offset int64) (int, error) {
	if err := f.reserve(offset + int64(len(content))); err != nil {
		return 0, err
	}
	return f.File.WriteAt(content, offset)
}

func (f *limitedFile) reserve(end int64) error {
	if f.limit > 0 && end > f.limit {
		return Permanent(CodeMediaTooLarge, fmt.Errorf("download exceeds limit %d", f.limit))
	}
	return nil
}
