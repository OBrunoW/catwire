package domain

import (
	"errors"
	"net/http"
)

type AppError struct {
	Status  int
	Code    string
	Message string
}

func (e *AppError) Error() string {
	return e.Code + ": " + e.Message
}

func NewAppError(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

func BadRequest(message string) *AppError {
	return NewAppError(http.StatusBadRequest, "BAD_REQUEST", message)
}

func Unauthorized(message string) *AppError {
	return NewAppError(http.StatusUnauthorized, "UNAUTHORIZED", message)
}

func NotFound(code, message string) *AppError {
	return NewAppError(http.StatusNotFound, code, message)
}

func InstanceNotFound() *AppError {
	return NotFound("INSTANCE_NOT_FOUND", "instance not found")
}

func MessageNotFound() *AppError {
	return NotFound("MESSAGE_NOT_FOUND", "message not found")
}

func ChatNotFound() *AppError {
	return NotFound("CHAT_NOT_FOUND", "chat not found")
}

func MediaNotFound() *AppError {
	return NotFound("MEDIA_NOT_FOUND", "media not found")
}

func WebhookNotFound() *AppError {
	return NotFound("WEBHOOK_NOT_FOUND", "webhook not found")
}

func DeliveryNotFound() *AppError {
	return NotFound("DELIVERY_NOT_FOUND", "delivery not found")
}

func RouteNotFound() *AppError {
	return NotFound("NOT_FOUND", "resource not found")
}

func InstanceAlreadyExists() *AppError {
	return NewAppError(http.StatusConflict, "INSTANCE_ALREADY_EXISTS", "external_id already in use")
}

func InstanceNotConnected() *AppError {
	return NewAppError(http.StatusConflict, "INSTANCE_NOT_CONNECTED", "instance is not connected")
}

func InstanceAlreadyConnected() *AppError {
	return NewAppError(http.StatusConflict, "INSTANCE_ALREADY_CONNECTED", "instance is already connected")
}

func InstanceNotPaired() *AppError {
	return NewAppError(http.StatusConflict, "INSTANCE_NOT_PAIRED", "instance is not paired")
}

func IdempotencyConflict() *AppError {
	return NewAppError(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key reused with a different body")
}

func MediaNotReady() *AppError {
	return NewAppError(http.StatusConflict, "MEDIA_NOT_READY", "media is not stored yet")
}

func QRExpired() *AppError {
	return NewAppError(http.StatusGone, "QR_EXPIRED", "qr code expired")
}

func MediaTooLarge() *AppError {
	return NewAppError(http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "media exceeds the size limit")
}

func UnsupportedMediaType(message string) *AppError {
	return NewAppError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", message)
}

func RecipientNotOnWhatsApp() *AppError {
	return NewAppError(http.StatusUnprocessableEntity, "RECIPIENT_NOT_ON_WHATSAPP", "recipient has no whatsapp account")
}

func MediaFetchFailed(message string) *AppError {
	return NewAppError(http.StatusUnprocessableEntity, "MEDIA_FETCH_FAILED", message)
}

func HistoryUnavailable() *AppError {
	return NewAppError(http.StatusUnprocessableEntity, "HISTORY_UNAVAILABLE", "no anchor message in the chat")
}

func WhatsAppError(message string) *AppError {
	return NewAppError(http.StatusBadGateway, "WHATSAPP_ERROR", message)
}

func ServiceUnavailable(message string) *AppError {
	return NewAppError(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", message)
}

func Internal() *AppError {
	return NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
}
