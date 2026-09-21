package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Auth.Mode != "local" {
		t.Errorf("expected mode local, got %s", cfg.Auth.Mode)
	}
}

func TestLoadConfigWithOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yamlData := `
server:
  port: 9090
  bind: "127.0.0.1"
database:
  path: "` + filepath.Join(tmpDir, "test.db") + `"
storage:
  logs_dir: "` + filepath.Join(tmpDir, "logs") + `"
`
	if err := os.WriteFile(configFile, []byte(yamlData), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.Bind != "127.0.0.1" {
		t.Errorf("expected bind 127.0.0.1, got %s", cfg.Server.Bind)
	}
	if cfg.I18n.DefaultLanguage != "en" {
		t.Errorf("expected default lang en, got %s", cfg.I18n.DefaultLanguage)
	}
}

func TestI18nConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yamlData := `
i18n:
  default_language: "de"
  locales_dir: "/path/to/locales"
`
	if err := os.WriteFile(configFile, []byte(yamlData), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.I18n.DefaultLanguage != "de" {
		t.Errorf("expected de, got %s", cfg.I18n.DefaultLanguage)
	}
	if cfg.I18n.LocalesDir != "/path/to/locales" {
		t.Errorf("expected /path/to/locales, got %s", cfg.I18n.LocalesDir)
	}
}

func TestEnvironmentConfig(t *testing.T) {
	tests := []struct {
		env           EnvironmentConfig
		expectedColor string
		expectedText  string
	}{
		{
			env:           EnvironmentConfig{Name: "Production"},
			expectedColor: "#dc2626",
			expectedText:  "#ffffff",
		},
		{
			env:           EnvironmentConfig{Name: "PROD-1", Color: "#ff0000"},
			expectedColor: "#ff0000",
			expectedText:  "#ffffff",
		},
		{
			env:           EnvironmentConfig{Name: "Test-Environment"},
			expectedColor: "#d97706",
			expectedText:  "#ffffff",
		},
		{
			env:           EnvironmentConfig{Name: "Dev-Stage"},
			expectedColor: "#d97706", // stage takes precedence or dev
			expectedText:  "#ffffff",
		},
		{
			env:           EnvironmentConfig{Name: "Development"},
			expectedColor: "#059669",
			expectedText:  "#ffffff",
		},
		{
			env:           EnvironmentConfig{Name: "Custom", Color: "#123456", TextColor: "#000000"},
			expectedColor: "#123456",
			expectedText:  "#000000",
		},
	}

	for _, tt := range tests {
		color := tt.env.EffectiveColor()
		if color != tt.expectedColor {
			t.Errorf("env %q: expected color %s, got %s", tt.env.Name, tt.expectedColor, color)
		}
		textColor := tt.env.EffectiveTextColor()
		if textColor != tt.expectedText {
			t.Errorf("env %q: expected text color %s, got %s", tt.env.Name, tt.expectedText, textColor)
		}
	}
}
