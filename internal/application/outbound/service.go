package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/OBrunoW/catwire/internal/application/media"
	"github.com/OBrunoW/catwire/internal/domain"
)

const (
	OriginAPI = "api"

	defaultUnattachedMediaTTL = 24 * time.Hour
)

type ServiceOptions struct {
	Unit               UnitOfWork
	Gateway            Gateway
	Fetcher            Fetcher
	Storage            media.Storage
	Converter          media.Converter
	MaxMediaBytes      int64
	UnattachedMediaTTL time.Duration
	RegisteredTTL      time.Duration
	UnregisteredTTL    time.Duration
	TempDir            string
	Logger             *slog.Logger
	Now                func() time.Time
}

type Service struct {
	unit          UnitOfWork
	gateway       Gateway
	fetcher       Fetcher
	storage       media.Storage
	converter     media.Converter
	maxMediaBytes int64
	unattachedTTL time.Duration
	tempDir       string
	logger        *slog.Logger
	now           func() time.Time
	recipients    *recipientCache
}

func NewService(options ServiceOptions) *Service {
	service := &Service{
		unit:          options.Unit,
		gateway:       options.Gateway,
		fetcher:       options.Fetcher,
		storage:       options.Storage,
		converter:     options.Converter,
		maxMediaBytes: options.MaxMediaBytes,
		unattachedTTL: options.UnattachedMediaTTL,
		tempDir:       options.TempDir,
		logger:        options.Logger,
		now:           options.Now,
	}
	if service.unattachedTTL <= 0 {
		service.unattachedTTL = defaultUnattachedMediaTTL
	}
	if service.tempDir == "" {
		service.tempDir = os.TempDir()
	}
	if service.logger == nil {
		service.logger = slog.Default()
	}
	if service.now == nil {
		service.now = time.Now
	}
	registeredTTL, unregisteredTTL := options.RegisteredTTL, options.UnregisteredTTL
	if registeredTTL <= 0 {
		registeredTTL = defaultRegisteredTTL
	}
	if unregisteredTTL <= 0 {
		unregisteredTTL = defaultUnregisteredTTL
	}
	service.recipients = newRecipientCache(registeredTTL, unregisteredTTL, service.now)
	return service
}

type SendResult struct {
	MessageView
	Replayed bool
}

type destination struct {
	chat *domain.Chat
	jid  string
}

func (s *Service) Send(
	ctx context.Context,
	clientID string,
	instanceID uuid.UUID,
	idempotencyKey string,
	request SendRequest,
) (SendResult, error) {
	if err := validateSend(idempotencyKey, request); err != nil {
		return SendResult{}, err
	}
	hash, err := request.Hash()
	if err != nil {
		return SendResult{}, err
	}
	instance, err := s.findInstance(ctx, clientID, instanceID)
	if err != nil {
		return SendResult{}, err
	}
	replayed, err := s.replay(ctx, instance.ID, idempotencyKey, hash)
	if err != nil || replayed != nil {
		return replayedResult(replayed), err
	}
	if instance.Status != domain.InstanceStatusConnected {
		return SendResult{}, domain.InstanceNotConnected()
	}
	return s.accept(ctx, clientID, *instance, idempotencyKey, hash, request)
}

func validateSend(idempotencyKey string, request SendRequest) error {
	if len(idempotencyKey) > MaxIdempotencyKeyLen {
		return domain.BadRequest("Idempotency-Key exceeds 200 characters")
	}
	return request.Validate()
}

func replayedResult(view *MessageView) SendResult {
	if view == nil {
		return SendResult{}
	}
	return SendResult{MessageView: *view, Replayed: true}
}

func (s *Service) findInstance(ctx context.Context, clientID string, instanceID uuid.UUID) (*domain.Instance, error) {
	var instance *domain.Instance
	err := s.unit.Do(ctx, func(unit Unit) error {
		found, err := unit.Instances.FindByID(ctx, clientID, instanceID)
		instance = found
		return err
	})
	return instance, err
}

func (s *Service) accept(
	ctx context.Context,
	clientID string,
	instance domain.Instance,
	idempotencyKey, hash string,
	request SendRequest,
) (SendResult, error) {
	target, err := s.resolveDestination(ctx, instance, request)
	if err != nil {
		return SendResult{}, err
	}
	replyTo, err := s.resolveReply(ctx, instance.ID, target, request.ReplyToMessageID)
	if err != nil {
		return SendResult{}, err
	}
	stored, err := s.mediaFor(ctx, clientID, instance, request)
	if err != nil {
		return SendResult{}, err
	}
	waMessageID, err := s.gateway.GenerateMessageID(instance.ID)
	if err != nil {
		return SendResult{}, mapGatewayError(err, "could not prepare the message")
	}
	message := s.buildMessage(instance, request, idempotencyKey, hash, waMessageID, replyTo)
	view, err := s.insert(ctx, instance, target, message, stored)
	if errors.Is(err, domain.ErrIdempotencyKeyTaken) {
		return s.concurrentReplay(ctx, instance.ID, idempotencyKey, hash)
	}
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageView: view}, nil
}

func (s *Service) mediaFor(ctx context.Context, clientID string, instance domain.Instance, request SendRequest) (*domain.Media, error) {
	if request.Media == nil {
		return nil, nil
	}
	return s.prepareMedia(ctx, clientID, instance, request)
}

func (s *Service) concurrentReplay(ctx context.Context, instanceID uuid.UUID, key, hash string) (SendResult, error) {
	replayed, err := s.replay(ctx, instanceID, key, hash)
	if err != nil {
		return SendResult{}, err
	}
	if replayed == nil {
		return SendResult{}, fmt.Errorf("outbound: idempotency key %q taken but message not found", key)
	}
	return replayedResult(replayed), nil
}

func (s *Service) replay(ctx context.Context, instanceID uuid.UUID, key, hash string) (*MessageView, error) {
	if key == "" {
		return nil, nil
	}
	var view *MessageView
	err := s.unit.Do(ctx, func(unit Unit) error {
		existing, err := unit.Messages.FindByIdempotencyKey(ctx, instanceID, key)
		if isMessageNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if existing.RequestHash == nil || *existing.RequestHash != hash {
			return domain.IdempotencyConflict()
		}
		loaded, err := loadView(ctx, unit, *existing)
		view = &loaded
		return err
	})
	return view, err
}

func (s *Service) resolveDestination(ctx context.Context, instance domain.Instance, request SendRequest) (destination, error) {
	if request.ChatID != nil {
		return s.destinationByChat(ctx, instance.ID, *request.ChatID)
	}
	parsed, needsLookup, err := parseRecipient(request.To)
	if err != nil {
		return destination{}, err
	}
	jid := parsed.jid
	if needsLookup {
		registration, err := s.lookupRecipient(ctx, instance.ID, parsed.phone)
		if err != nil {
			return destination{}, err
		}
		jid = registration.JID
	}
	return s.destinationByJID(ctx, instance.ID, jid)
}

func (s *Service) destinationByChat(ctx context.Context, instanceID, chatID uuid.UUID) (destination, error) {
	var target destination
	err := s.unit.Do(ctx, func(unit Unit) error {
		view, err := unit.Chats.FindByID(ctx, instanceID, chatID)
		if err != nil {
			return err
		}
		chat := view.Chat
		target = destination{chat: &chat, jid: chat.JID}
		return nil
	})
	return target, err
}

func (s *Service) destinationByJID(ctx context.Context, instanceID uuid.UUID, jid string) (destination, error) {
	target := destination{jid: jid}
	err := s.unit.Do(ctx, func(unit Unit) error {
		chat, err := findChat(ctx, unit.Chats, instanceID, jid)
		target.chat = chat
		return err
	})
	return target, err
}

func findChat(ctx context.Context, chats ChatStore, instanceID uuid.UUID, jid string) (*domain.Chat, error) {
	chat, err := chats.FindByJID(ctx, instanceID, jid)
	if err == nil {
		return chat, nil
	}
	if !isChatNotFound(err) {
		return nil, err
	}
	chat, err = chats.FindByLID(ctx, instanceID, jid)
	if isChatNotFound(err) {
		return nil, nil
	}
	return chat, err
}

func isChatNotFound(err error) bool {
	var appError *domain.AppError
	return errors.As(err, &appError) && appError.Code == domain.ChatNotFound().Code
}

func (s *Service) lookupRecipient(ctx context.Context, instanceID uuid.UUID, phone string) (Registration, error) {
	registration, cached := s.recipients.get(phone)
	if !cached {
		found, err := s.gateway.IsOnWhatsApp(ctx, instanceID, phone)
		if err != nil {
			return Registration{}, mapGatewayError(err, "could not check the recipient on whatsapp")
		}
		registration = found
		s.recipients.put(phone, registration)
	}
	if !registration.Exists {
		return Registration{}, domain.RecipientNotOnWhatsApp()
	}
	return registration, nil
}

func mapGatewayError(err error, message string) error {
	if errors.Is(err, ErrInstanceDisconnected) {
		return domain.InstanceNotConnected()
	}
	return domain.WhatsAppError(message)
}

func (s *Service) resolveReply(ctx context.Context, instanceID uuid.UUID, target destination, replyID *uuid.UUID) (*domain.Message, error) {
	if replyID == nil {
		return nil, nil
	}
	if target.chat == nil {
		return nil, domain.MessageNotFound()
	}
	var found *domain.Message
	err := s.unit.Do(ctx, func(unit Unit) error {
		message, err := unit.Messages.FindByID(ctx, instanceID, *replyID)
		found = message
		return err
	})
	if err != nil {
		return nil, err
	}
	if found.ChatID != target.chat.ID {
		return nil, domain.MessageNotFound()
	}
	return found, nil
}

func (s *Service) buildMessage(
	instance domain.Instance,
	request SendRequest,
	idempotencyKey, hash, waMessageID string,
	replyTo *domain.Message,
) domain.Message {
	now := s.now()
	message := domain.Message{
		ID:             uuid.New(),
		InstanceID:     instance.ID,
		WAMessageID:    waMessageID,
		Direction:      domain.DirectionOutbound,
		Origin:         OriginAPI,
		Source:         "live",
		SenderJID:      stringValue(instance.JID),
		SenderPhone:    instance.Phone,
		SenderLID:      instance.LID,
		SenderPushName: instance.PushName,
		Type:           request.Type,
		Text:           textOf(request),
		Status:         domain.MessageStatusPending,
		RequestHash:    &hash,
		Timestamp:      now,
	}
	if idempotencyKey != "" {
		message.IdempotencyKey = &idempotencyKey
	}
	if replyTo != nil {
		message.ReplyToWAMessageID = &replyTo.WAMessageID
	}
	message.Metadata = metadataOf(request.Metadata)
	return message
}

func textOf(request SendRequest) *string {
	if request.Text == nil || *request.Text == "" {
		return nil
	}
	return request.Text
}

func metadataOf(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return raw
}

func (s *Service) insert(
	ctx context.Context,
	instance domain.Instance,
	target destination,
	message domain.Message,
	attached *domain.Media,
) (MessageView, error) {
	var view MessageView
	err := s.unit.Do(ctx, func(unit Unit) error {
		chat, err := s.ensureChat(ctx, unit, instance, target)
		if err != nil {
			return err
		}
		message.ChatID = chat.ID
		inserted, err := unit.Messages.Insert(ctx, &message)
		if err != nil {
			return err
		}
		if !inserted {
			return fmt.Errorf("outbound: generated message id %s already exists", message.WAMessageID)
		}
		if attached != nil {
			if err := unit.Media.Attach(ctx, instance.ID, attached.ID, message.ID); err != nil {
				return err
			}
		}
		if err := unit.Chats.TouchLastMessage(ctx, instance.ID, chat.ID, message.ID, message.Timestamp); err != nil {
			return err
		}
		view, err = loadView(ctx, unit, message)
		return err
	})
	return view, err
}

func (s *Service) ensureChat(ctx context.Context, unit Unit, instance domain.Instance, target destination) (domain.Chat, error) {
	if target.chat != nil {
		return *target.chat, nil
	}
	chat := domain.Chat{ID: uuid.New(), InstanceID: instance.ID, JID: target.jid, Type: domain.ChatTypeDirect}
	if phone, ok := phoneOfJID(target.jid); ok {
		chat.Phone = &phone
	}
	if err := unit.Chats.Upsert(ctx, &chat); err != nil {
		return domain.Chat{}, err
	}
	return chat, nil
}

func phoneOfJID(jid string) (string, bool) {
	parsed, needsLookup, err := parseRecipient(jid)
	if err != nil || !needsLookup {
		return "", false
	}
	return parsed.phone, true
}
