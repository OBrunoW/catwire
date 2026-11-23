package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/ingestion"
	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	CodeSendExpired = "SEND_EXPIRED"
	CodeSendFailed  = "SEND_FAILED"

	defaultPollInterval = time.Second
	defaultMaxAge       = 10 * time.Minute

	expiredMessage = "message expired before it could be sent"
	failedMessage  = "whatsapp could not send the message"
)

var defaultRetryBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}

type WorkerOptions struct {
	Unit         UnitOfWork
	Gateway      Gateway
	Storage      media.Storage
	MaxAge       time.Duration
	PollInterval time.Duration
	Backoff      []time.Duration
	Logger       *slog.Logger
	Now          func() time.Time
}

type Worker struct {
	WorkerOptions
	mu       sync.Mutex
	inFlight map[uuid.UUID]struct{}
	running  sync.WaitGroup
}

func NewWorker(options WorkerOptions) *Worker {
	if options.MaxAge <= 0 {
		options.MaxAge = defaultMaxAge
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if len(options.Backoff) == 0 {
		options.Backoff = defaultRetryBackoff
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Worker{WorkerOptions: options, inFlight: map[uuid.UUID]struct{}{}}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	defer w.running.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.dispatch(ctx); err != nil {
				w.Logger.Error("send worker dispatch failed", "error", err)
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	if err := w.dispatch(ctx); err != nil {
		return err
	}
	w.running.Wait()
	return nil
}

func (w *Worker) dispatch(ctx context.Context) error {
	var instanceIDs []uuid.UUID
	err := w.Unit.Do(ctx, func(unit Unit) error {
		ids, err := unit.Messages.ListInstancesWithPendingSend(ctx)
		instanceIDs = ids
		return err
	})
	if err != nil {
		return err
	}
	for _, instanceID := range instanceIDs {
		if w.claim(instanceID) {
			w.running.Add(1)
			go w.runInstance(ctx, instanceID)
		}
	}
	return nil
}

func (w *Worker) claim(instanceID uuid.UUID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, busy := w.inFlight[instanceID]; busy {
		return false
	}
	w.inFlight[instanceID] = struct{}{}
	return true
}

func (w *Worker) release(instanceID uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.inFlight, instanceID)
}

func (w *Worker) runInstance(ctx context.Context, instanceID uuid.UUID) {
	defer w.running.Done()
	defer w.release(instanceID)
	for ctx.Err() == nil {
		proceed, err := w.processHead(ctx, instanceID)
		if err != nil {
			w.Logger.Error("send worker failed", "instance_id", instanceID, "error", err)
			return
		}
		if !proceed {
			return
		}
	}
}

type queueEntry struct {
	instance domain.Instance
	message  domain.Message
	view     MessageView
}

func (w *Worker) processHead(ctx context.Context, instanceID uuid.UUID) (bool, error) {
	entry, found, err := w.loadHead(ctx, instanceID)
	if err != nil || !found {
		return false, err
	}
	now := w.Now()
	if now.Sub(entry.message.CreatedAt) > w.MaxAge {
		return true, w.fail(ctx, entry, CodeSendExpired, expiredMessage)
	}
	if entry.message.NextAttemptAt != nil && entry.message.NextAttemptAt.After(now) {
		return false, nil
	}
	if entry.instance.Status != domain.InstanceStatusConnected {
		return false, nil
	}
	return w.attempt(ctx, entry)
}

func (w *Worker) loadHead(ctx context.Context, instanceID uuid.UUID) (queueEntry, bool, error) {
	var entry queueEntry
	var found bool
	err := w.Unit.Do(ctx, func(unit Unit) error {
		pending, err := unit.Messages.ListPendingForSend(ctx, instanceID, 1)
		if err != nil || len(pending) == 0 {
			return err
		}
		instance, err := unit.Instances.FindUnscoped(ctx, instanceID)
		if err != nil {
			return err
		}
		view, err := loadView(ctx, unit, pending[0])
		if err != nil {
			return err
		}
		entry = queueEntry{instance: *instance, message: pending[0], view: view}
		found = true
		return nil
	})
	return entry, found, err
}

func (w *Worker) attempt(ctx context.Context, entry queueEntry) (bool, error) {
	receipt, err := w.send(ctx, entry)
	if err == nil {
		return true, w.markSent(ctx, entry, receipt)
	}
	if ctx.Err() != nil {
		return false, nil
	}
	if errors.Is(err, ErrInstanceDisconnected) {
		return false, nil
	}
	var permanent *PermanentError
	if errors.As(err, &permanent) {
		return true, w.fail(ctx, entry, permanent.Code, permanent.Message)
	}
	w.Logger.Warn("send attempt failed", "message_id", entry.message.ID, "attempt", entry.message.SendAttempts+1, "error_type", fmt.Sprintf("%T", err))
	return w.handleTransient(ctx, entry)
}

func (w *Worker) handleTransient(ctx context.Context, entry queueEntry) (bool, error) {
	failures := entry.message.SendAttempts
	if failures >= len(w.Backoff) {
		return true, w.fail(ctx, entry, CodeSendFailed, failedMessage)
	}
	next := w.Now().Add(w.Backoff[failures])
	err := w.Unit.Do(ctx, func(unit Unit) error {
		return unit.Messages.ScheduleSendRetry(ctx, entry.message.InstanceID, entry.message.ID, next)
	})
	return false, err
}

func (w *Worker) send(ctx context.Context, entry queueEntry) (SendReceipt, error) {
	command, closer, err := w.buildCommand(ctx, entry)
	if err != nil {
		return SendReceipt{}, err
	}
	defer closer()
	return w.Gateway.Send(ctx, command)
}

func (w *Worker) buildCommand(ctx context.Context, entry queueEntry) (SendCommand, func(), error) {
	command := SendCommand{
		InstanceID:  entry.message.InstanceID,
		ChatJID:     entry.view.ChatJID,
		WAMessageID: entry.message.WAMessageID,
		Type:        entry.message.Type,
		Text:        entry.message.Text,
	}
	noop := func() {}
	reply, err := w.replyReference(ctx, entry)
	if err != nil {
		return SendCommand{}, noop, err
	}
	command.Reply = reply
	if entry.message.Type == MessageTypeText {
		return command, noop, nil
	}
	payload, closer, err := w.mediaPayload(ctx, entry.view.Media)
	if err != nil {
		return SendCommand{}, noop, err
	}
	command.Media = payload
	return command, closer, nil
}

func (w *Worker) replyReference(ctx context.Context, entry queueEntry) (*ReplyReference, error) {
	if entry.message.ReplyToWAMessageID == nil {
		return nil, nil
	}
	reference := &ReplyReference{WAMessageID: *entry.message.ReplyToWAMessageID}
	err := w.Unit.Do(ctx, func(unit Unit) error {
		target, err := unit.Messages.FindByChatAndWAMessageID(ctx, entry.message.InstanceID, entry.message.ChatID, reference.WAMessageID)
		if isMessageNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		reference.SenderJID = target.SenderJID
		reference.Text = target.Text
		return nil
	})
	return reference, err
}

func (w *Worker) mediaPayload(ctx context.Context, item *domain.Media) (*MediaPayload, func(), error) {
	if item == nil || item.StorageKey == nil || item.Status != domain.MediaStatusStored {
		return nil, nil, &PermanentError{Code: "MEDIA_UNAVAILABLE", Message: "media is no longer available"}
	}
	body, err := w.Storage.Open(ctx, *item.StorageKey)
	if errors.Is(err, media.ErrObjectNotFound) {
		return nil, nil, &PermanentError{Code: "MEDIA_UNAVAILABLE", Message: "media is no longer available"}
	}
	if err != nil {
		return nil, nil, err
	}
	payload := &MediaPayload{
		MimeType:  stringValue(item.MimeType),
		FileName:  stringValue(item.FileName),
		VoiceNote: item.IsVoiceNote,
		Body:      io.Reader(body),
	}
	if item.SizeBytes != nil {
		payload.Size = *item.SizeBytes
	}
	return payload, func() { body.Close() }, nil
}

func (w *Worker) markSent(ctx context.Context, entry queueEntry, receipt SendReceipt) error {
	sentAt := receipt.Timestamp
	if sentAt.IsZero() {
		sentAt = w.Now()
	}
	return w.Unit.Do(ctx, func(unit Unit) error {
		_, advanced, err := unit.Messages.AdvanceStatus(ctx, entry.message.InstanceID, entry.message.ID, domain.MessageStatusSent, sentAt)
		if err != nil {
			return err
		}
		if !advanced {
			emitted, err := w.sentEventAlreadyEmitted(ctx, unit, entry)
			if err != nil || emitted {
				return err
			}
		}
		return w.emit(ctx, unit, entry, ingestion.EventTypeMessageSent)
	})
}

func (w *Worker) sentEventAlreadyEmitted(ctx context.Context, unit Unit, entry queueEntry) (bool, error) {
	current, err := unit.Messages.FindByID(ctx, entry.message.InstanceID, entry.message.ID)
	if err != nil {
		return false, err
	}
	if current.Status == domain.MessageStatusPending || current.Status == domain.MessageStatusFailed {
		return true, nil
	}
	return current.SentAt != nil, nil
}

func (w *Worker) fail(ctx context.Context, entry queueEntry, code, message string) error {
	return w.Unit.Do(ctx, func(unit Unit) error {
		marked, err := unit.Messages.MarkFailed(ctx, entry.message.InstanceID, entry.message.ID, code, message)
		if err != nil || !marked {
			return err
		}
		return w.emit(ctx, unit, entry, EventTypeMessageFailed)
	})
}

func (w *Worker) emit(ctx context.Context, unit Unit, entry queueEntry, eventType string) error {
	current, err := unit.Messages.FindByID(ctx, entry.message.InstanceID, entry.message.ID)
	if err != nil {
		return err
	}
	view := entry.view
	view.Message = *current
	event, err := newMessageEvent(entry.instance, eventType, view, w.Now())
	if err != nil {
		return err
	}
	return unit.Outbox.Insert(ctx, event)
}
