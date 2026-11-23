package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	apihttp "github.com/OBrunoW/catwire/internal/adapters/http"
	"github.com/OBrunoW/catwire/internal/domain"
	"github.com/OBrunoW/catwire/internal/middleware"
)

type Dependencies struct {
	Auth              func(http.Handler) http.Handler
	Instances         InstanceService
	Sessions          SessionManager
	Settings          SettingsService
	Contacts          ContactsService
	Sender            MessageSender
	MessageQueries    MessageQueries
	ChatQueries       ChatQueries
	Reads             ChatReader
	Presence          PresenceSender
	History           HistoryRequester
	Profiles          ProfileService
	Privacy           PrivacyService
	Library           MediaLibrary
	Content           MediaContent
	SignedMediaAccess bool
	Webhooks          WebhookService
	Health            HealthChecks
	MaxMediaBytes     int64
	MessageBodyLimit  int64
}

func NewRouter(deps Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID, middleware.Recover)
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apihttp.WriteError(w, domain.RouteNotFound())
	})
	health := NewHealthHandler(deps.Health)
	router.Get("/health", health.Health)
	router.Get("/ready", health.Ready)
	router.Route("/api/v1", func(api chi.Router) {
		mountMediaContent(api, deps)
		api.Group(func(protected chi.Router) {
			protected.Use(deps.Auth)
			mountInstances(protected, deps)
			mountInstanceResources(protected, deps)
			mountChats(protected, deps)
			mountMedia(protected, deps)
			mountWebhooks(protected, deps)
		})
	})
	return router
}

func newMediaHandler(deps Dependencies) *MediaHandler {
	return NewMediaHandler(deps.Library, deps.Content, deps.SignedMediaAccess, deps.MaxMediaBytes)
}

func mountMediaContent(api chi.Router, deps Dependencies) {
	api.Method(http.MethodGet, "/media/{media_id}/content", newMediaHandler(deps).Content(deps.Auth))
}

func mountInstances(router chi.Router, deps Dependencies) {
	instances := NewInstancesHandler(deps.Instances, deps.Sessions)
	router.Post("/instances", instances.Create)
	router.Get("/instances", instances.List)
	router.Get("/instances/{instance_id}", instances.Get)
	router.Patch("/instances/{instance_id}", instances.Update)
	router.Delete("/instances/{instance_id}", instances.Delete)
	router.Post("/instances/{instance_id}/connect", instances.Connect)
	router.Get("/instances/{instance_id}/qrcode", instances.QR)
	router.Post("/instances/{instance_id}/pair-code", instances.PairCode)
	router.Post("/instances/{instance_id}/disconnect", instances.Disconnect)
	router.Post("/instances/{instance_id}/logout", instances.Logout)
}

func mountInstanceResources(router chi.Router, deps Dependencies) {
	settings := NewSettingsHandler(deps.Settings)
	router.Get("/instances/{instance_id}/settings", settings.Get)
	router.Patch("/instances/{instance_id}/settings", settings.Update)
	profile := NewProfileHandler(deps.Profiles)
	router.Get("/instances/{instance_id}/profile", profile.Get)
	router.Patch("/instances/{instance_id}/profile", profile.Update)
	privacy := NewPrivacyHandler(deps.Privacy)
	router.Get("/instances/{instance_id}/privacy", privacy.Get)
	router.Patch("/instances/{instance_id}/privacy", privacy.Update)
	contacts := NewContactsHandler(deps.Contacts)
	router.Post("/instances/{instance_id}/contacts/check", contacts.Check)
	messages := NewMessagesHandler(deps.Sender, deps.MessageQueries, deps.MessageBodyLimit)
	router.Post("/instances/{instance_id}/messages", messages.Send)
	router.Get("/instances/{instance_id}/messages", messages.List)
	router.Get("/instances/{instance_id}/messages/{message_id}", messages.Get)
}

func mountChats(router chi.Router, deps Dependencies) {
	chats := NewChatsHandler(deps.ChatQueries, deps.Reads)
	router.Get("/instances/{instance_id}/chats", chats.List)
	router.Get("/instances/{instance_id}/chats/{chat_id}", chats.Get)
	router.Post("/instances/{instance_id}/chats/{chat_id}/read", chats.MarkRead)
	presence := NewPresenceHandler(deps.Presence)
	router.Post("/instances/{instance_id}/chats/{chat_id}/presence", presence.Send)
	history := NewHistoryHandler(deps.History)
	router.Post("/instances/{instance_id}/chats/{chat_id}/history", history.Request)
}

func mountMedia(router chi.Router, deps Dependencies) {
	media := newMediaHandler(deps)
	router.Post("/instances/{instance_id}/media", media.Upload)
	router.Get("/media/{media_id}", media.Get)
}

func mountWebhooks(router chi.Router, deps Dependencies) {
	webhooks := NewWebhooksHandler(deps.Webhooks)
	router.Post("/webhooks", webhooks.Create)
	router.Get("/webhooks", webhooks.List)
	router.Get("/webhooks/{webhook_id}", webhooks.Get)
	router.Patch("/webhooks/{webhook_id}", webhooks.Update)
	router.Delete("/webhooks/{webhook_id}", webhooks.Delete)
	router.Post("/webhooks/{webhook_id}/rotate-secret", webhooks.RotateSecret)
	router.Post("/webhooks/{webhook_id}/test", webhooks.SendTest)
	router.Get("/webhooks/{webhook_id}/deliveries", webhooks.ListDeliveries)
	router.Post("/webhooks/{webhook_id}/deliveries/{delivery_id}/retry", webhooks.RetryDelivery)
}
