package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	chatJIDUniqueIndex = "chats_instance_jid_uq"
	chatLIDUniqueIndex = "chats_instance_lid_uq"
)

const chatColumns = "id, instance_id, jid, lid, phone, type, name, last_message_id, last_message_at, created_at, updated_at"

const chatViewSelect = `SELECT ` +
	"c.id, c.instance_id, c.jid, c.lid, c.phone, c.type, c.name, c.last_message_id, c.last_message_at, c.created_at, c.updated_at, " +
	`m.id, m.type, m.text, m.direction, m.wa_timestamp
	FROM catwire.chats c
	LEFT JOIN catwire.messages m ON m.id = c.last_message_id`

type ChatRepository struct {
	db DBTX
}

func NewChatRepository(db DBTX) *ChatRepository {
	return &ChatRepository{db: db}
}

func scanChat(row scanner) (domain.Chat, error) {
	var chat domain.Chat
	err := row.Scan(
		&chat.ID, &chat.InstanceID, &chat.JID, &chat.LID, &chat.Phone, &chat.Type, &chat.Name,
		&chat.LastMessageID, &chat.LastMessageAt, &chat.CreatedAt, &chat.UpdatedAt,
	)
	return chat, err
}

func scanChatView(row scanner) (domain.ChatView, error) {
	var view domain.ChatView
	var lastID *uuid.UUID
	var lastType, lastDirection *string
	var lastText *string
	var lastTimestamp *time.Time
	err := row.Scan(
		&view.ID, &view.InstanceID, &view.JID, &view.LID, &view.Phone, &view.Type, &view.Name,
		&view.LastMessageID, &view.LastMessageAt, &view.CreatedAt, &view.UpdatedAt,
		&lastID, &lastType, &lastText, &lastDirection, &lastTimestamp,
	)
	if err != nil {
		return view, err
	}
	if lastID != nil && lastType != nil && lastDirection != nil && lastTimestamp != nil {
		view.LastMessage = &domain.LastMessageSummary{
			ID: *lastID, Type: *lastType, Text: lastText, Direction: *lastDirection, Timestamp: *lastTimestamp,
		}
	}
	return view, nil
}

func (r *ChatRepository) Upsert(ctx context.Context, chat *domain.Chat) error {
	row := r.db.QueryRow(ctx,
		`INSERT INTO catwire.chats (id, instance_id, jid, lid, phone, type, name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (instance_id, jid) DO UPDATE SET
			lid = COALESCE(EXCLUDED.lid, catwire.chats.lid),
			phone = COALESCE(EXCLUDED.phone, catwire.chats.phone),
			name = COALESCE(EXCLUDED.name, catwire.chats.name)
		RETURNING `+chatColumns,
		chat.ID, chat.InstanceID, chat.JID, chat.LID, chat.Phone, chat.Type, chat.Name,
	)
	stored, err := scanChat(row)
	if isChatIdentityConflict(err) {
		return ErrChatIdentityConflict
	}
	if err != nil {
		return wrapError("upsert chat", err)
	}
	*chat = stored
	return nil
}

func (r *ChatRepository) FindByID(ctx context.Context, instanceID, id uuid.UUID) (*domain.ChatView, error) {
	row := r.db.QueryRow(ctx,
		chatViewSelect+` WHERE c.id = $1 AND c.instance_id = $2`,
		id, instanceID,
	)
	view, err := scanChatView(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ChatNotFound()
	}
	if err != nil {
		return nil, wrapError("find chat", err)
	}
	return &view, nil
}

func (r *ChatRepository) FindByJID(ctx context.Context, instanceID uuid.UUID, jid string) (*domain.Chat, error) {
	return r.findOne(ctx, "find chat by jid", "jid", instanceID, jid)
}

func (r *ChatRepository) FindByLID(ctx context.Context, instanceID uuid.UUID, lid string) (*domain.Chat, error) {
	return r.findOne(ctx, "find chat by lid", "lid", instanceID, lid)
}

func (r *ChatRepository) findOne(
	ctx context.Context,
	operation string,
	column string,
	instanceID uuid.UUID,
	value string,
) (*domain.Chat, error) {
	statement := `SELECT ` + chatColumns + ` FROM catwire.chats WHERE instance_id = $1 AND ` + column + ` = $2`
	chat, err := scanChat(r.db.QueryRow(ctx, statement, instanceID, value))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ChatNotFound()
	}
	if err != nil {
		return nil, wrapError(operation, err)
	}
	return &chat, nil
}

func (r *ChatRepository) ListByInstance(
	ctx context.Context,
	instanceID uuid.UUID,
	filter domain.ChatFilter,
	params apihttp.PageParams,
) (apihttp.PageResult[domain.ChatView], error) {
	builder := &queryBuilder{}
	builder.where("c.instance_id = " + builder.arg(instanceID))
	if filter.Type != "" {
		builder.where("c.type = " + builder.arg(filter.Type))
	}
	if filter.Search != "" {
		pattern := builder.arg(likePattern(filter.Search))
		builder.where("(c.name ILIKE " + pattern + " OR c.phone ILIKE " + pattern + ")")
	}
	var position optionalTimePosition
	hasCursor, err := decodeCursor(params, &position)
	if err != nil {
		return apihttp.PageResult[domain.ChatView]{}, err
	}
	if hasCursor {
		builder.where(chatCursorClause(builder, position))
	}
	return queryPage(ctx, r.db, builder, chatViewSelect,
		"c.last_message_at DESC NULLS LAST, c.id DESC", params, scanChatView,
		func(last domain.ChatView) any { return optionalTimePosition{At: last.LastMessageAt, ID: last.ID} },
	)
}

func chatCursorClause(builder *queryBuilder, position optionalTimePosition) string {
	idArg := builder.arg(position.ID)
	if position.At == nil {
		return "(c.last_message_at IS NULL AND c.id < " + idArg + ")"
	}
	atArg := builder.arg(*position.At)
	return "(c.last_message_at IS NULL OR c.last_message_at < " + atArg +
		" OR (c.last_message_at = " + atArg + " AND c.id < " + idArg + "))"
}

func isChatIdentityConflict(err error) bool {
	constraint, unique := uniqueConstraint(err)
	return unique && (constraint == chatJIDUniqueIndex || constraint == chatLIDUniqueIndex)
}

func (r *ChatRepository) UpdateJID(ctx context.Context, instanceID, id uuid.UUID, jid string, phone *string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE catwire.chats SET jid = $3, phone = COALESCE($4, phone) WHERE instance_id = $1 AND id = $2`,
		instanceID, id, jid, phone,
	)
	if isChatIdentityConflict(err) {
		return ErrChatIdentityConflict
	}
	if err != nil {
		return wrapError("update chat jid", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ChatNotFound()
	}
	return nil
}

func (r *ChatRepository) TouchLastMessage(ctx context.Context, instanceID, id, messageID uuid.UUID, at time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE catwire.chats SET last_message_id = $3, last_message_at = $4
		WHERE instance_id = $1 AND id = $2 AND (last_message_at IS NULL OR last_message_at <= $4)`,
		instanceID, id, messageID, at,
	)
	if err != nil {
		return wrapError("touch chat last message", err)
	}
	return nil
}
