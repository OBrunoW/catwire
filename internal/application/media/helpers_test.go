package media

import (
	"bytes"
	"io"
)

type nopSeekCloser struct {
	*bytes.Reader
}

func (nopSeekCloser) Close() error { return nil }

func bytesReader(content []byte) *bytes.Reader { return bytes.NewReader(content) }

var _ io.ReadSeekCloser = nopSeekCloser{}
