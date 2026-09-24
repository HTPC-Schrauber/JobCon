package runner

import (
	"context"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "''"},
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"$USER", "'$USER'"},
		{"$(reboot)", "'$(reboot)'"},
		{"`whoami`", "'`whoami`'"},
		{"foo'bar", `'foo'\''bar'`},
		{`a"b\c`, `'a"b\c'`},
		{"param; rm -rf /", "'param; rm -rf /'"},
	}

	for _, tt := range tests {
		got := ShellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("ShellQuote(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestValidation(t *testing.T) {
	// Version validation
	validVersions := []string{"1.0.0", "v2.1-beta", "3.0_patch1", "build.123"}
	for _, v := range validVersions {
		if err := ValidateVersion(v); err != nil {
			t.Errorf("expected version %q to be valid, got: %v", v, err)
		}
	}

	invalidVersions := []string{"", "1.0; rm -rf", "1.0$(whoami)", "1.0`id`", "1.0\n2.0", "1.0 2.0"}
	for _, v := range invalidVersions {
		if err := ValidateVersion(v); err == nil {
			t.Errorf("expected version %q to be rejected, got nil error", v)
		}
	}

	// Context validation
	validContexts := []string{"", "Default", "PROD", "QA_1", "stage-test"}
	for _, c := range validContexts {
		if err := ValidateContext(c); err != nil {
			t.Errorf("expected context %q to be valid, got: %v", c, err)
		}
	}

	invalidContexts := []string{"Default; whoami", "$(cat /etc/passwd)", "Default`test`", "ctx 1"}
	for _, c := range invalidContexts {
		if err := ValidateContext(c); err == nil {
			t.Errorf("expected context %q to be rejected, got nil error", c)
		}
	}

	// Param Key validation
	validKeys := []string{"param1", "MY_VAR", "log.level", "db-host"}
	for _, k := range validKeys {
		if err := ValidateParamKey(k); err != nil {
			t.Errorf("expected param key %q to be valid, got: %v", k, err)
		}
	}

	invalidKeys := []string{"", "key; evil", "key=val", "$VAR", "key`id`"}
	for _, k := range invalidKeys {
		if err := ValidateParamKey(k); err == nil {
			t.Errorf("expected param key %q to be rejected, got nil error", k)
		}
	}

	// Param Value validation (null bytes)
	if err := ValidateParamValue("hello world\n123"); err != nil {
		t.Errorf("expected multiline string to pass value validation: %v", err)
	}
	if err := ValidateParamValue("hello\x00world"); err == nil {
		t.Errorf("expected null-byte string to be rejected")
	}

	// EnvFile validation
	validEnvFiles := []string{"", "/opt/talend/.env", "/home/user/job.env", "relative/path.env"}
	for _, e := range validEnvFiles {
		if err := ValidateEnvFile(e); err != nil {
			t.Errorf("expected env file %q to be valid, got: %v", e, err)
		}
	}

	invalidEnvFiles := []string{"/opt/.env; rm -rf /", "/opt/.env$(id)", "/opt/.env`whoami`", "/opt/test\n.env"}
	for _, e := range invalidEnvFiles {
		if err := ValidateEnvFile(e); err == nil {
			t.Errorf("expected env file %q to be rejected, got nil error", e)
		}
	}
}

func TestStartExecution_RejectsInjection(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	server := &db.Server{ID: "srv-sec", Name: "Sec Server", Host: "127.0.0.1", SSHKeyPath: "/k", Status: "online"}
	_ = database.CreateServer(server)
	job := &db.Job{ID: "job-sec", Name: "Sec Job", ServerID: "srv-sec", GroupID: "g", ArtifactID: "sec_job", ActiveVersion: "1.0.0"}
	_ = database.CreateJob(job)

	m := NewExecutionManager(database, nil, nil, &config.NexusConfig{})

	// Malicious version
	_, err = m.StartExecution(context.Background(), "job-sec", "run", "1.0.0; rm -rf /", "Default", nil, "test")
	if err == nil || !strings.Contains(err.Error(), "ungültiges Versionsformat") {
		t.Errorf("expected invalid version error, got: %v", err)
	}

	// Malicious context
	_, err = m.StartExecution(context.Background(), "job-sec", "run", "1.0.0", "Default$(reboot)", nil, "test")
	if err == nil || !strings.Contains(err.Error(), "ungültiger Kontextname") {
		t.Errorf("expected invalid context error, got: %v", err)
	}

	// Malicious param key
	_, err = m.StartExecution(context.Background(), "job-sec", "run", "1.0.0", "Default", map[string]string{
		"key; dangerous": "value",
	}, "test")
	if err == nil || !strings.Contains(err.Error(), "ungültiger Parameter-Schlüssel") {
		t.Errorf("expected invalid param key error, got: %v", err)
	}
}
