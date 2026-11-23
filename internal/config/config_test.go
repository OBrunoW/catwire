package config

import (
	"reflect"
	"testing"
)

var managedVariables = []string{
	"APP_ENV", "PORT", "DATABASE_URL", "SERVICE_KEYS", "API_KEY", "KAFKA_BROKERS",
	"PUBLIC_BASE_URL", "MEDIA_DIR", "MEDIA_MAX_BYTES", "MEDIA_URL_SECRET",
	"SEND_MAX_AGE_SECONDS", "WEBHOOK_MAX_ATTEMPTS", "WEBHOOK_TIMEOUT_SECONDS",
}

func setEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range managedVariables {
		t.Setenv(name, "")
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func TestLoadFailures(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{"invalid app env", map[string]string{"APP_ENV": "staging", "API_KEY": "k"}},
		{"no keys in production", map[string]string{"APP_ENV": "production"}},
		{"no keys with default env", map[string]string{}},
		{"service keys without colon", map[string]string{"SERVICE_KEYS": "crm"}},
		{"service keys with empty name", map[string]string{"SERVICE_KEYS": ":secret"}},
		{"service keys with empty key", map[string]string{"SERVICE_KEYS": "crm:"}},
		{"public base url without secret", map[string]string{"APP_ENV": "development", "PUBLIC_BASE_URL": "https://x.test"}},
		{"non numeric integer", map[string]string{"APP_ENV": "development", "MEDIA_MAX_BYTES": "abc"}},
		{"zero integer", map[string]string{"APP_ENV": "development", "SEND_MAX_AGE_SECONDS": "0"}},
		{"negative integer", map[string]string{"APP_ENV": "development", "WEBHOOK_MAX_ATTEMPTS": "-3"}},
		{"invalid webhook timeout", map[string]string{"APP_ENV": "development", "WEBHOOK_TIMEOUT_SECONDS": "1.5"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setEnvironment(t, test.values)
			if _, err := Load(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestLoadDevelopmentWithoutKeys(t *testing.T) {
	setEnvironment(t, map[string]string{"APP_ENV": "development"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsDevelopment() {
		t.Error("expected development environment")
	}
	if cfg.AuthConfigured() {
		t.Error("expected auth not configured")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	setEnvironment(t, map[string]string{"API_KEY": "secret"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []struct {
		name     string
		got      any
		expected any
	}{
		{"app env", cfg.AppEnv, "production"},
		{"port", cfg.Port, "5001"},
		{"media dir", cfg.MediaDir, "/data/media"},
		{"media max bytes", cfg.MediaMaxBytes, int64(104857600)},
		{"send max age", cfg.SendMaxAgeSeconds, 600},
		{"webhook attempts", cfg.WebhookMaxAttempts, 10},
		{"webhook timeout", cfg.WebhookTimeoutSeconds, 10},
	}
	for _, check := range checks {
		if check.got != check.expected {
			t.Errorf("%s: got %v, expected %v", check.name, check.got, check.expected)
		}
	}
}

func TestLoadReadsConfiguredValues(t *testing.T) {
	setEnvironment(t, map[string]string{
		"APP_ENV":          "development",
		"PORT":             "9000",
		"SERVICE_KEYS":     "crm:abc, platform:def",
		"API_KEY":          "external",
		"KAFKA_BROKERS":    "a:9092, b:9092",
		"PUBLIC_BASE_URL":  "https://x.test/",
		"MEDIA_URL_SECRET": "signing",
		"MEDIA_MAX_BYTES":  "2048",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedKeys := []ServiceKey{{Name: "crm", Key: "abc"}, {Name: "platform", Key: "def"}}
	if !reflect.DeepEqual(cfg.ServiceKeys, expectedKeys) {
		t.Errorf("service keys: got %v", cfg.ServiceKeys)
	}
	if !reflect.DeepEqual(cfg.KafkaBrokers, []string{"a:9092", "b:9092"}) {
		t.Errorf("kafka brokers: got %v", cfg.KafkaBrokers)
	}
	if cfg.PublicBaseURL != "https://x.test" {
		t.Errorf("public base url: got %q", cfg.PublicBaseURL)
	}
	if cfg.Port != "9000" || cfg.MediaMaxBytes != 2048 || cfg.APIKey != "external" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}
