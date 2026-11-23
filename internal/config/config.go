package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	envDevelopment = "development"
	envProduction  = "production"

	placeholderSecretPrefix = "changeme"
	minMediaURLSecretLength = 32
)

type ServiceKey struct {
	Name string
	Key  string
}

type Config struct {
	AppEnv                string
	Port                  string
	DatabaseURL           string
	ServiceKeys           []ServiceKey
	APIKey                string
	KafkaBrokers          []string
	PublicBaseURL         string
	MediaDir              string
	MediaMaxBytes         int64
	MediaURLSecret        string
	MediaURLTTLSeconds    int
	SendMaxAgeSeconds     int
	WebhookMaxAttempts    int
	WebhookTimeoutSeconds int
	OutboxRetentionDays   int
	FFmpegPath            string
}

func (c Config) IsDevelopment() bool {
	return c.AppEnv == envDevelopment
}

func (c Config) AuthConfigured() bool {
	return len(c.ServiceKeys) > 0 || c.APIKey != ""
}

func Load() (Config, error) {
	serviceKeys, err := parseServiceKeys(os.Getenv("SERVICE_KEYS"))
	if err != nil {
		return Config{}, err
	}
	mediaMaxBytes, err := intFromEnv("MEDIA_MAX_BYTES", 104857600)
	if err != nil {
		return Config{}, err
	}
	sendMaxAge, err := intFromEnv("SEND_MAX_AGE_SECONDS", 600)
	if err != nil {
		return Config{}, err
	}
	webhookAttempts, err := intFromEnv("WEBHOOK_MAX_ATTEMPTS", 10)
	if err != nil {
		return Config{}, err
	}
	webhookTimeout, err := intFromEnv("WEBHOOK_TIMEOUT_SECONDS", 10)
	if err != nil {
		return Config{}, err
	}
	mediaURLTTL, err := intFromEnv("MEDIA_URL_TTL_SECONDS", 3600)
	if err != nil {
		return Config{}, err
	}
	outboxRetention, err := intFromEnv("OUTBOX_RETENTION_DAYS", 7)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		AppEnv:                stringFromEnv("APP_ENV", envProduction),
		Port:                  stringFromEnv("PORT", "5001"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		ServiceKeys:           serviceKeys,
		APIKey:                os.Getenv("API_KEY"),
		KafkaBrokers:          splitList(os.Getenv("KAFKA_BROKERS")),
		PublicBaseURL:         strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		MediaDir:              stringFromEnv("MEDIA_DIR", "/data/media"),
		MediaMaxBytes:         int64(mediaMaxBytes),
		MediaURLSecret:        os.Getenv("MEDIA_URL_SECRET"),
		MediaURLTTLSeconds:    mediaURLTTL,
		SendMaxAgeSeconds:     sendMaxAge,
		WebhookMaxAttempts:    webhookAttempts,
		WebhookTimeoutSeconds: webhookTimeout,
		OutboxRetentionDays:   outboxRetention,
		FFmpegPath:            stringFromEnv("FFMPEG_PATH", "ffmpeg"),
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.AppEnv != envDevelopment && c.AppEnv != envProduction {
		return fmt.Errorf("APP_ENV must be %q or %q", envDevelopment, envProduction)
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if !c.AuthConfigured() && !c.IsDevelopment() {
		return errors.New("SERVICE_KEYS or API_KEY is required outside development")
	}
	if c.PublicBaseURL != "" && c.MediaURLSecret == "" {
		return errors.New("MEDIA_URL_SECRET is required when PUBLIC_BASE_URL is set")
	}
	if c.PublicBaseURL != "" && len(c.MediaURLSecret) < minMediaURLSecretLength {
		return fmt.Errorf("MEDIA_URL_SECRET must have at least %d characters when PUBLIC_BASE_URL is set", minMediaURLSecretLength)
	}
	if c.AppEnv == envProduction {
		return c.rejectPlaceholderSecrets()
	}
	return nil
}

func (c Config) rejectPlaceholderSecrets() error {
	if isPlaceholderSecret(c.MediaURLSecret) {
		return errors.New("MEDIA_URL_SECRET must not use a placeholder value in production")
	}
	if isPlaceholderSecret(c.APIKey) {
		return errors.New("API_KEY must not use a placeholder value in production")
	}
	for _, serviceKey := range c.ServiceKeys {
		if isPlaceholderSecret(serviceKey.Key) {
			return errors.New("SERVICE_KEYS must not use a placeholder value in production")
		}
	}
	return nil
}

func isPlaceholderSecret(value string) bool {
	return strings.HasPrefix(value, placeholderSecretPrefix)
}

func parseServiceKeys(raw string) ([]ServiceKey, error) {
	var keys []ServiceKey
	for _, entry := range splitList(raw) {
		name, key, found := strings.Cut(entry, ":")
		name, key = strings.TrimSpace(name), strings.TrimSpace(key)
		if !found || name == "" || key == "" {
			return nil, errors.New("SERVICE_KEYS must use the format name:key,name:key")
		}
		keys = append(keys, ServiceKey{Name: name, Key: key})
	}
	return keys, nil
}

func splitList(raw string) []string {
	var items []string
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func stringFromEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func intFromEnv(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
