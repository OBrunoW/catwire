package media

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/domain"
)

const groupServer = "@g.us"

type RetryResult struct {
	DirectPath string
	Failure    string
}

func (w *Worker) requestRetry(ctx context.Context, item domain.Media) error {
	target, err := w.retryTarget(ctx, item)
	if err != nil {
		return w.scheduleRetry(ctx, item, redact(err.Error(), item))
	}
	if err := w.Retries.RequestRetry(ctx, item.InstanceID, target); err != nil {
		return w.scheduleRetry(ctx, item, redact(err.Error(), item))
	}
	next := w.Now().Add(w.RetryWait)
	return w.Unit.Do(ctx, func(unit Unit) error {
		return unit.Media.RecordDownloadFailure(ctx, item.ID, retryRequestedMessage, &next)
	})
}

func (w *Worker) retryTarget(ctx context.Context, item domain.Media) (RetryTarget, error) {
	if item.MessageID == nil {
		return RetryTarget{}, errors.New("media is not attached to a message")
	}
	var target RetryTarget
	err := w.Unit.Do(ctx, func(unit Unit) error {
		found, err := w.loadOwner(ctx, unit, item)
		if err != nil {
			return err
		}
		target = RetryTarget{
			WAMessageID: found.message.WAMessageID,
			ChatJID:     found.chat.JID,
			SenderJID:   found.message.SenderJID,
			FromMe:      found.message.Direction == domain.DirectionOutbound,
			MediaKey:    item.WAMediaKey,
		}
		return nil
	})
	return target, err
}

func (t RetryTarget) IsGroup() bool {
	return strings.HasSuffix(t.ChatJID, groupServer)
}

func (w *Worker) HandleRetry(
	ctx context.Context,
	instanceID uuid.UUID,
	waMessageID string,
	decrypt func(mediaKey []byte) (RetryResult, error),
) error {
	item, err := w.findAwaitingRetry(ctx, instanceID, waMessageID)
	if err != nil || item == nil {
		return err
	}
	result, err := decrypt(item.WAMediaKey)
	if err != nil {
		return w.terminal(ctx, *item, CodeMediaUnavailable, redact(err.Error(), *item))
	}
	if result.Failure != "" || result.DirectPath == "" {
		return w.terminal(ctx, *item, CodeMediaUnavailable, redact(result.Failure, *item))
	}
	return w.Unit.Do(ctx, func(unit Unit) error {
		return unit.Media.ApplyRetryPath(ctx, item.ID, result.DirectPath)
	})
}

func (w *Worker) findAwaitingRetry(ctx context.Context, instanceID uuid.UUID, waMessageID string) (*domain.Media, error) {
	var awaiting *domain.Media
	err := w.Unit.Do(ctx, func(unit Unit) error {
		message, err := unit.Messages.FindByWAMessageID(ctx, instanceID, waMessageID)
		if isMessageNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		items, err := unit.Media.FindByMessageIDs(ctx, []uuid.UUID{message.ID})
		if err != nil {
			return err
		}
		for index := range items {
			if isAwaitingRetry(items[index]) {
				awaiting = &items[index]
			}
		}
		return nil
	})
	return awaiting, err
}

func isAwaitingRetry(item domain.Media) bool {
	return item.Status == domain.MediaStatusPending && item.LastError != nil && *item.LastError == retryRequestedMessage
}

func isMessageNotFound(err error) bool {
	appError, ok := domain.AsAppError(err)
	return ok && appError.Code == domain.MessageNotFound().Code
}
