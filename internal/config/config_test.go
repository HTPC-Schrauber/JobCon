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
}
