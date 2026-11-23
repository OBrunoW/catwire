package media

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

const downloadFailedMessage = "download failed"

type downloadFailure struct {
	kind string
}

func (e *downloadFailure) Error() string { return downloadFailedMessage }

func SanitizeDownloadError(err error) error {
	if err == nil {
		return nil
	}
	var permanent *permanentError
	switch {
	case errors.As(err, &permanent):
		return permanent
	case errors.Is(err, ErrInstanceDisconnected):
		return ErrInstanceDisconnected
	case errors.Is(err, ErrMediaGone):
		return ErrMediaGone
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	return &downloadFailure{kind: describeErrorKind(err)}
}

func describeErrorKind(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Sprintf("*url.Error op=%s cause=%T", urlErr.Op, urlErr.Err)
	}
	return fmt.Sprintf("%T", err)
}

func errorKind(err error) string {
	var failure *downloadFailure
	if errors.As(err, &failure) {
		return failure.kind
	}
	return describeErrorKind(err)
}
