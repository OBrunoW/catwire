package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	StorageBackendLocal = "local"

	defaultBatchSize    = 10
	defaultLease        = 10 * time.Minute
	defaultMaxAttempts  = 6
	defaultRetryWait    = 2 * time.Minute
	disconnectedWait    = time.Minute
	defaultPollInterval = 2 * time.Second
	defaultURLTTL       = time.Hour

	retryRequestedMessage = "media retry requested from phone"
)

var defaultBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

type Options struct {
	Unit          UnitOfWork
	Storage       Storage
	Downloader    Downloader
	Converter     Converter
	Retries       RetryRequester
	Signer        *Signer
	PublicBaseURL string
	URLTTL        time.Duration
	MaxBytes      int64
	BatchSize     int
	Lease         time.Duration
	MaxAttempts   int
	RetryWait     time.Duration
	PollInterval  time.Duration
	Backoff       []time.Duration
	TempDir       string
	Logger        *slog.Logger
	Now           func() time.Time
}

type Worker struct {
	Options
}

func NewWorker(options Options) *Worker {
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.Lease <= 0 {
		options.Lease = defaultLease
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = defaultMaxAttempts
	}
	if options.RetryWait <= 0 {
		options.RetryWait = defaultRetryWait
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.URLTTL <= 0 {
		options.URLTTL = defaultURLTTL
	}
	if len(options.Backoff) == 0 {
		options.Backoff = defaultBackoff
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Worker{Options: options}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drain(ctx)
		}
	}
}

func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := w.RunOnce(ctx)
		if err != nil {
			w.Logger.Error("media worker claim failed", "error", err)
			return
		}
		if processed < w.BatchSize {
			return
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	var claimed []domain.Media
	err := w.Unit.Do(ctx, func(unit Unit) error {
		items, err := unit.Media.ClaimPending(ctx, w.BatchSize, w.Lease)
		claimed = items
		return err
	})
	if err != nil {
		return 0, err
	}
	for _, item := range claimed {
		if ctx.Err() != nil {
			break
		}
		w.handle(ctx, item)
	}
	return len(claimed), nil
}

func (w *Worker) handle(ctx context.Context, item domain.Media) {
	err := w.process(ctx, item)
	if err == nil {
		return
	}
	if errors.Is(err, ErrInstanceDisconnected) {
		w.deferUntilConnected(ctx, item)
		return
	}
	w.Logger.Warn("media download failed", "media_id", item.ID, "attempt", item.DownloadAttempts+1, "error_type", errorKind(err))
	if recordErr := w.recordFailure(ctx, item, err); recordErr != nil {
		w.Logger.Error("media failure bookkeeping failed", "media_id", item.ID, "error", recordErr)
	}
}

func (w *Worker) deferUntilConnected(ctx context.Context, item domain.Media) {
	w.Logger.Info("media deferred, instance is not connected", "media_id", item.ID, "instance_id", item.InstanceID)
	next := w.Now().Add(disconnectedWait)
	err := w.Unit.Do(ctx, func(unit Unit) error {
		return unit.Media.Reschedule(ctx, item.ID, next)
	})
	if err != nil {
		w.Logger.Error("media reschedule failed", "media_id", item.ID, "error", err)
	}
}

func (w *Worker) process(ctx context.Context, item domain.Media) error {
	if item.LastError != nil && *item.LastError == retryRequestedMessage {
		return Permanent(CodeMediaUnavailable, errors.New("no response to the media retry request"))
	}
	if err := w.validate(item); err != nil {
		return err
	}
	stored, err := w.fetchAndStore(ctx, item)
	if err != nil {
		return err
	}
	return w.markStored(ctx, item, stored)
}

func (w *Worker) validate(item domain.Media) error {
	if item.WADirectPath == nil || *item.WADirectPath == "" || len(item.WAMediaKey) == 0 {
		return Permanent(CodeMediaUnavailable, errors.New("media has no download credentials"))
	}
	if item.SizeBytes != nil && w.MaxBytes > 0 && *item.SizeBytes > w.MaxBytes {
		return Permanent(CodeMediaTooLarge, fmt.Errorf("declared size %d exceeds limit %d", *item.SizeBytes, w.MaxBytes))
	}
	return nil
}

func (w *Worker) recordFailure(ctx context.Context, item domain.Media, cause error) error {
	var permanent *permanentError
	if errors.As(cause, &permanent) {
		return w.terminal(ctx, item, permanent.code, redact(cause.Error(), item))
	}
	exhausted := item.DownloadAttempts+1 >= w.MaxAttempts
	if exhausted {
		code := CodeMediaDownloadFailed
		if errors.Is(cause, ErrMediaGone) {
			code = CodeMediaUnavailable
		}
		return w.terminal(ctx, item, code, redact(cause.Error(), item))
	}
	if errors.Is(cause, ErrMediaGone) && w.Retries != nil {
		return w.requestRetry(ctx, item)
	}
	return w.scheduleRetry(ctx, item, redact(cause.Error(), item))
}

func (w *Worker) scheduleRetry(ctx context.Context, item domain.Media, message string) error {
	next := w.Now().Add(w.backoffFor(item.DownloadAttempts))
	return w.Unit.Do(ctx, func(unit Unit) error {
		return unit.Media.RecordDownloadFailure(ctx, item.ID, message, &next)
	})
}

func (w *Worker) backoffFor(attempts int) time.Duration {
	if attempts >= len(w.Backoff) {
		return w.Backoff[len(w.Backoff)-1]
	}
	return w.Backoff[attempts]
}

func (w *Worker) terminal(ctx context.Context, item domain.Media, code, detail string) error {
	return w.Unit.Do(ctx, func(unit Unit) error {
		if err := unit.Media.RecordDownloadFailure(ctx, item.ID, detail, nil); err != nil {
			return err
		}
		return w.emitFailed(ctx, unit, item, code, detail)
	})
}

func (w *Worker) emitFailed(ctx context.Context, unit Unit, item domain.Media, code, detail string) error {
	if item.MessageID == nil {
		return nil
	}
	found, err := w.loadOwner(ctx, unit, item)
	if err != nil {
		return err
	}
	item.Status = domain.MediaStatusFailed
	data := MediaFailedData{
		MediaEventData: found.eventData(newMediaResource(item)),
		Error:          domain.ErrorResource{Code: code, Message: failureMessage(code)},
	}
	return w.insertEvent(ctx, unit, found, EventTypeMediaFailed, data)
}

func (w *Worker) markStored(ctx context.Context, item domain.Media, stored domain.StoredMedia) error {
	return w.Unit.Do(ctx, func(unit Unit) error {
		if err := unit.Media.MarkStored(ctx, item.ID, stored); err != nil {
			return err
		}
		if item.MessageID == nil {
			return nil
		}
		found, err := w.loadOwner(ctx, unit, item)
		if err != nil {
			return err
		}
		resource := newMediaResource(applyStored(item, stored, w.Now()))
		w.attachDownloadURL(&resource)
		return w.insertEvent(ctx, unit, found, EventTypeMediaStored, found.eventData(resource))
	})
}

func (w *Worker) attachDownloadURL(resource *domain.MediaResource) {
	if w.Signer == nil || w.PublicBaseURL == "" {
		return
	}
	link, expiresAt := w.Signer.SignedURL(w.PublicBaseURL, resource.ID, w.URLTTL)
	resource.DownloadURL = &link
	resource.DownloadURLExpiresAt = &expiresAt
}

func applyStored(item domain.Media, stored domain.StoredMedia, at time.Time) domain.Media {
	item.Status = domain.MediaStatusStored
	item.MimeType = &stored.MimeType
	item.SizeBytes = &stored.SizeBytes
	item.SHA256 = &stored.SHA256
	item.StoredAt = &at
	return item
}

func (w *Worker) loadOwner(ctx context.Context, unit Unit, item domain.Media) (owner, error) {
	message, err := unit.Messages.FindByID(ctx, item.InstanceID, *item.MessageID)
	if err != nil {
		return owner{}, err
	}
	chat, err := unit.Chats.FindByID(ctx, item.InstanceID, message.ChatID)
	if err != nil {
		return owner{}, err
	}
	instance, err := unit.Instances.FindUnscoped(ctx, item.InstanceID)
	if err != nil {
		return owner{}, err
	}
	return owner{message: *message, chat: chat.Chat, instance: *instance}, nil
}

func (w *Worker) insertEvent(ctx context.Context, unit Unit, found owner, eventType string, data any) error {
	event, err := newOutboxEvent(found, eventType, data, w.Now())
	if err != nil {
		return fmt.Errorf("media: encode %s payload: %w", eventType, err)
	}
	return unit.Outbox.Insert(ctx, event)
}
