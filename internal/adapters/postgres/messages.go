package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	messageColumns = "id, instance_id, chat_id, wa_message_id, direction, origin, source, sender_jid, sender_phone, " +
		"sender_lid, sender_push_name, type, text, content, reply_to_wa_message_id, is_forwarded, is_ephemeral, " +
		"is_view_once, status, error_code, error_message, reactions, metadata, idempotency_key, request_hash, " +
		"send_attempts, next_attempt_at, wa_timestamp, sent_at, delivered_at, read_at, played_at, edited_at, " +
		"revoked_at, created_at, updated_at"

	messageIdempotencyUniqueIndex = "messages_instance_idempotency_uq"

	statusRankOfPrevious = `CASE previous.status WHEN 'pending' THEN 0 WHEN 'received' THEN 0 WHEN 'sent' THEN 1 ` +
		`WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 WHEN 'played' THEN 4 ELSE 99 END`
	statusRankOfNew = `CASE $3::text WHEN 'pending' THEN 0 WHEN 'received' THEN 0 WHEN 'sent' THEN 1 ` +
		`WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 WHEN 'played' THEN 4 ELSE -1 END`

	advanceStatusSQL = `UPDATE catwire.messages AS target
		SET status = $3::text,
			sent_at = CASE WHEN $3::text = 'sent' THEN COALESCE(target.sent_at, $4::timestamptz) ELSE target.sent_at END,
			delivered_at = CASE WHEN $3::text = 'delivered' THEN COALESCE(target.delivered_at, $4::timestamptz) ELSE target.delivered_at END,
			read_at = CASE WHEN $3::text = 'read' THEN COALESCE(target.read_at, $4::timestamptz) ELSE target.read_at END,
			played_at = CASE WHEN $3::text = 'played' THEN COALESCE(target.played_at, $4::timestamptz) ELSE target.played_at END
		FROM previous
		WHERE target.id = previous.id AND ` + statusRankOfPrevious + ` < ` + statusRankOfNew + `
		RETURNING target.id, target.chat_id, previous.status`
)

type MessageRepository struct {
	db DBTX
}

func NewMessageRepository(db DBTX) *MessageRepository {
	return &MessageRepository{db: db}
}

func scanMessage(row scanner) (domain.Message, error) {
	var message domain.Message
	err := row.Scan(
		&message.ID, &message.InstanceID, &message.ChatID, &message.WAMessageID, &message.Direction,
		&message.Origin, &message.Source, &message.SenderJID, &message.SenderPhone, &message.SenderLID,
		&message.SenderPushName, &message.Type, &message.Text, &message.Content, &message.ReplyToWAMessageID,
		&message.IsForwarded, &message.IsEphemeral, &message.IsViewOnce, &message.Status, &message.ErrorCode,
		&message.ErrorMessage, &message.Reactions, &message.Metadata, &message.IdempotencyKey,
		&message.RequestHash, &message.SendAttempts, &message.NextAttemptAt, &message.Timestamp,
		&message.SentAt, &message.DeliveredAt, &message.ReadAt, &message.PlayedAt, &message.EditedAt,
		&message.RevokedAt, &message.CreatedAt, &message.UpdatedAt,
	)
	return message, err
}

func (r *MessageRepository) Insert(ctx context.Context, message *domain.Message) (bool, error) {
	err := r.db.QueryRow(ctx,
		`INSERT INTO catwire.messages (
			id, instance_id, chat_id, wa_message_id, direction, origin, source, sender_jid, sender_phone,
			sender_lid, sender_push_name, type, text, content, reply_to_wa_message_id, is_forwarded,
			is_ephemeral, is_view_once, status, error_code, error_message, reactions, metadata,
			idempotency_key, request_hash, next_attempt_at, wa_timestamp
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
			$21, $22, $23, $24, $25, $26, $27
		)
		ON CONFLICT (instance_id, chat_id, wa_message_id) DO NOTHING
		RETURNING created_at, updated_at`,
		message.ID, message.InstanceID, message.ChatID, message.WAMessageID, message.Direction, message.Origin,
		message.Source, message.SenderJID, message.SenderPhone, message.SenderLID, message.SenderPushName,
		message.Type, message.Text, orEmptyJSON(message.Content, "{}"), message.ReplyToWAMessageID,
		message.IsForwarded, message.IsEphemeral, message.IsViewOnce, message.Status, message.ErrorCode,
		message.ErrorMessage, orEmptyJSON(message.Reactions, "[]"), orEmptyJSON(message.Metadata, "{}"),
		message.IdempotencyKey, message.RequestHash, message.NextAttemptAt, message.Timestamp,
	).Scan(&message.CreatedAt, &message.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if constraint, unique := uniqueConstraint(err); unique && constraint == messageIdempotencyUniqueIndex {
		return false, ErrIdempotencyKeyTaken
	}
	if err != nil {
		return false, wrapError("insert message", err)
	}
	return true, nil
}

func (r *MessageRepository) FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.Message, error) {
	return r.findOne(ctx, "find message", `instance_id = $1 AND id = $2`, instanceID, id)
}

func (r *MessageRepository) FindByWAMessageID(ctx context.Context, instanceID uuid.UUID, waMessageID string) (*domain.Message, error) {
	return r.findOne(ctx, "find message by wa id",
		`instance_id = $1 AND wa_message_id = $2 ORDER BY created_at DESC, id DESC LIMIT 1`,
		instanceID, waMessageID,
	)
}

func (r *MessageRepository) FindByChatAndWAMessageID(
	ctx context.Context,
	instanceID, chatID uuid.UUID,
	waMessageID string,
) (*domain.Message, error) {
	return r.findOne(ctx, "find message by chat and wa id",
		`instance_id = $1 AND chat_id = $2 AND wa_message_id = $3`,
		instanceID, chatID, waMessageID,
	)
}

func (r *MessageRepository) FindByIdempotencyKey(ctx context.Context, instanceID uuid.UUID, key string) (*domain.Message, error) {
	return r.findOne(ctx, "find message by idempotency key",
		`instance_id = $1 AND idempotency_key = $2`,
		instanceID, key,
	)
}

func (r *MessageRepository) findOne(ctx context.Context, operation, condition string, args ...any) (*domain.Message, error) {
	row := r.db.QueryRow(ctx, `SELECT `+messageColumns+` FROM catwire.messages WHERE `+condition, args...)
	message, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.MessageNotFound()
	}
	if err != nil {
		return nil, wrapError(operation, err)
	}
	return &message, nil
}

func (r *MessageRepository) List(
	ctx context.Context,
	instanceID uuid.UUID,
	filter domain.MessageFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.Message], error) {
	builder := &queryBuilder{}
	builder.where("instance_id = " + builder.arg(instanceID))
	applyMessageFilter(builder, filter)
	var position timePosition
	hasCursor, err := decodeCursor(params, &position)
	if err != nil {
		return apihttp.PageResult[domain.Message]{}, err
	}
	if hasCursor {
		builder.where("(wa_timestamp, id) < (" + builder.arg(position.At) + ", " + builder.arg(position.ID) + ")")
	}
	return queryPage(ctx, r.db, builder,
		`SELECT `+messageColumns+` FROM catwire.messages`,
		"wa_timestamp DESC, id DESC", params, scanMessage,
		func(last domain.Message) any { return timePosition{At: last.Timestamp, ID: last.ID} },
	)
}

func applyMessageFilter(builder *queryBuilder, filter domain.MessageFilter) {
	if filter.ChatID != nil {
		builder.where("chat_id = " + builder.arg(*filter.ChatID))
	}
	if filter.WAMessageID != "" {
		builder.where("wa_message_id = " + builder.arg(filter.WAMessageID))
	}
	if filter.Direction != "" {
		builder.where("direction = " + builder.arg(filter.Direction))
	}
	if filter.Type != "" {
		builder.where("type = " + builder.arg(filter.Type))
	}
	if filter.Source != "" {
		builder.where("source = " + builder.arg(filter.Source))
	}
	if filter.Since != nil {
		builder.where("wa_timestamp >= " + builder.arg(*filter.Since))
	}
	if filter.Until != nil {
		builder.where("wa_timestamp <= " + builder.arg(*filter.Until))
	}
	if filter.Search != "" {
		builder.where("text ILIKE " + builder.arg(likePattern(filter.Search)))
	}
}

func (r *MessageRepository) AdvanceStatus(
	ctx context.Context,
	instanceID, id uuid.UUID,
	status string,
	at time.Time,
) (domain.StatusAdvance, bool, error) {
	advances, err := r.advance(ctx, `instance_id = $1 AND id = $2`, instanceID, id, status, at)
	if err != nil {
		return domain.StatusAdvance{}, false, err
	}
	if len(advances) == 0 {
		return domain.StatusAdvance{}, false, nil
	}
	return advances[0], true, nil
}

func (r *MessageRepository) AdvanceStatusByWAMessageID(
	ctx context.Context,
	instanceID uuid.UUID,
	waMessageID string,
	status string,
	at time.Time,
) ([]domain.StatusAdvance, error) {
	return r.advance(ctx, `instance_id = $1 AND wa_message_id = $2`, instanceID, waMessageID, status, at)
}

func (r *MessageRepository) advance(
	ctx context.Context,
	condition string,
	instanceID any,
	target any,
	status string,
	at time.Time,
) ([]domain.StatusAdvance, error) {
	rows, err := r.db.Query(ctx,
		`WITH previous AS (
			SELECT id, status FROM catwire.messages WHERE `+condition+` FOR UPDATE
		) `+advanceStatusSQL,
		instanceID, target, status, at,
	)
	if err != nil {
		return nil, wrapError("advance message status", err)
	}
	advances, err := collect(rows, scanStatusAdvance)
	if err != nil {
		return nil, wrapError("advance message status", err)
	}
	return advances, nil
}

func scanStatusAdvance(row scanner) (domain.StatusAdvance, error) {
	var advance domain.StatusAdvance
	err := row.Scan(&advance.MessageID, &advance.ChatID, &advance.PreviousStatus)
	return advance, err
}

func (r *MessageRepository) MarkFailed(ctx context.Context, instanceID, id uuid.UUID, code, message string) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.messages
		SET status = 'failed', error_code = $3, error_message = $4, next_attempt_at = NULL
		WHERE instance_id = $1 AND id = $2 AND status = 'pending'`,
		instanceID, id, code, message,
	)
	if err != nil {
		return false, wrapError("mark message failed", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *MessageRepository) ListPendingForSend(ctx context.Context, instanceID uuid.UUID, limit int) ([]domain.Message, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+qualify("candidate", messageColumns)+` FROM catwire.messages AS candidate
		WHERE candidate.instance_id = $1 AND candidate.status = 'pending' AND candidate.direction = 'outbound'
			AND NOT EXISTS (
				SELECT 1 FROM catwire.messages AS earlier
				WHERE earlier.instance_id = candidate.instance_id
					AND earlier.status = 'pending' AND earlier.direction = 'outbound'
					AND (earlier.created_at, earlier.id) < (candidate.created_at, candidate.id)
			)
		ORDER BY candidate.created_at, candidate.id
		LIMIT $2`,
		instanceID, limit,
	)
	if err != nil {
		return nil, wrapError("list pending messages", err)
	}
	messages, err := collect(rows, scanMessage)
	if err != nil {
		return nil, wrapError("list pending messages", err)
	}
	return messages, nil
}

func (r *MessageRepository) ListInstancesWithPendingSend(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT instance_id FROM catwire.messages WHERE status = 'pending' AND direction = 'outbound'`,
	)
	if err != nil {
		return nil, wrapError("list instances with pending messages", err)
	}
	ids, err := collect(rows, func(row scanner) (uuid.UUID, error) {
		var id uuid.UUID
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		return nil, wrapError("list instances with pending messages", err)
	}
	return ids, nil
}

func (r *MessageRepository) ScheduleSendRetry(ctx context.Context, instanceID, id uuid.UUID, nextAttemptAt time.Time) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.messages
		SET send_attempts = send_attempts + 1, next_attempt_at = $3
		WHERE instance_id = $1 AND id = $2 AND status = 'pending'`,
		instanceID, id, nextAttemptAt,
	)
	if err != nil {
		return wrapError("schedule message retry", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.MessageNotFound()
	}
	return nil
}

func (r *MessageRepository) ReplaceUndecryptable(
	ctx context.Context,
	instanceID, id uuid.UUID,
	update domain.MessageContentUpdate,
) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.messages SET type = $3, text = $4, content = $5
		WHERE instance_id = $1 AND id = $2 AND type = 'undecryptable'`,
		instanceID, id, update.Type, update.Text, orEmptyJSON(update.Content, "{}"),
	)
	if err != nil {
		return false, wrapError("replace undecryptable message", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *MessageRepository) ApplyEdit(
	ctx context.Context,
	instanceID, id uuid.UUID,
	text *string,
	content json.RawMessage,
	at time.Time,
) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.messages SET text = $3, content = $4, edited_at = $5
		WHERE instance_id = $1 AND id = $2 AND revoked_at IS NULL`,
		instanceID, id, text, orEmptyJSON(content, "{}"), at,
	)
	if err != nil {
		return false, wrapError("apply message edit", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *MessageRepository) ApplyRevocation(ctx context.Context, instanceID, id uuid.UUID, at time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.messages SET revoked_at = $3
		WHERE instance_id = $1 AND id = $2 AND revoked_at IS NULL`,
		instanceID, id, at,
	)
	if err != nil {
		return false, wrapError("apply message revocation", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *MessageRepository) ApplyReaction(
	ctx context.Context,
	instanceID, id uuid.UUID,
	reaction domain.Reaction,
	removed bool,
) (bool, error) {
	encoded, err := json.Marshal(reaction)
	if err != nil {
		return false, wrapError("encode reaction", err)
	}
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.messages
		SET reactions = (
			SELECT COALESCE(jsonb_agg(existing), '[]'::jsonb)
			FROM jsonb_array_elements(reactions) AS existing
			WHERE existing ->> 'sender_jid' <> $3::text
		) || CASE WHEN $4::boolean THEN '[]'::jsonb ELSE jsonb_build_array($5::jsonb) END
		WHERE instance_id = $1 AND id = $2`,
		instanceID, id, reaction.SenderJID, removed, encoded,
	)
	if err != nil {
		return false, wrapError("apply message reaction", err)
	}
	return tag.RowsAffected() == 1, nil
}
