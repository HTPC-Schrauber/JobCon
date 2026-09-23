package runner

import (
	"context"
	"jobcon/internal/db"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSSHRunner_ScriptsDir(t *testing.T) {
	r1 := NewSSHRunner("/tmp/key", 10, 10)
	if r1.scriptsDir != "./scripts" {
		t.Errorf("expected default scriptsDir './scripts', got %q", r1.scriptsDir)
	}

	r2 := NewSSHRunner("/tmp/key", 10, 10, "/custom/scripts")
	if r2.scriptsDir != "/custom/scripts" {
		t.Errorf("expected custom scriptsDir '/custom/scripts', got %q", r2.scriptsDir)
	}
}

func TestSetupServer_MissingScriptsDir(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistent := filepath.Join(tmpDir, "missing_scripts")

	runner := NewSSHRunner("/tmp/key", 1, 1, nonExistent)
	server := &db.Server{
		Host: "127.0.0.1",
		Port: 22,
		User: "root",
	}

	res, err := runner.SetupServer(context.Background(), server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Errorf("expected SetupServer to fail when scripts directory does not exist")
	}
	if !strings.Contains(res.ErrorMessage, "Lokales Skriptverzeichnis") {
		t.Errorf("expected error message about local script directory, got %q", res.ErrorMessage)
	}
}

func TestSetupServer_MissingJobconCtl(t *testing.T) {
	tmpDir := t.TempDir()
	// Create scripts dir with only run_job.sh, but missing jobcon_ctl.sh
	_ = os.WriteFile(filepath.Join(tmpDir, "run_job.sh"), []byte("#!/bin/bash\necho run\n"), 0755)

	runner := NewSSHRunner("/tmp/key", 1, 1, tmpDir)
	server := &db.Server{
		Host: "127.0.0.1",
		Port: 22,
		User: "root",
	}

	res, err := runner.SetupServer(context.Background(), server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Errorf("expected SetupServer to fail when jobcon_ctl.sh is missing")
	}
	if !strings.Contains(res.ErrorMessage, "jobcon_ctl.sh") {
		t.Errorf("expected error message to mention jobcon_ctl.sh, got %q", res.ErrorMessage)
	}
}

func TestBuildClientConfig_MissingKeyDiagnostics(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create a dummy key in fake HOME/.ssh/
	sshDir := filepath.Join(tmpHome, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("failed to create fake .ssh dir: %v", err)
	}
	testKeyPath := filepath.Join(sshDir, "id_test.key")
	if err := os.WriteFile(testKeyPath, []byte("fake-key"), 0600); err != nil {
		t.Fatalf("failed to write test key: %v", err)
	}

	runner := NewSSHRunner("", 5, 5)

	// Case 1: Server configured with host path /home/otheruser/.ssh/id_test.key
	// Candidate exists in current HOME/.ssh/id_test.key -> should include hint
	serverWithHostPath := &db.Server{
		Name:       "Testserver",
		Host:       "127.0.0.1",
		SSHKeyPath: "/home/otheruser/.ssh/id_test.key",
	}
	_, err := runner.buildClientConfig(serverWithHostPath)
	if err == nil {
		t.Fatal("expected error for non-existent key, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "SSH private key file not found at \"/home/otheruser/.ssh/id_test.key\"") {
		t.Errorf("unexpected error message: %q", errMsg)
	}
	if !strings.Contains(errMsg, "found matching key file at") {
		t.Errorf("expected error message to include candidate hint, got: %q", errMsg)
	}

	// Case 2: Server configured with non-existent key where no candidate exists
	serverNoCandidate := &db.Server{
		Name:       "Testserver",
		Host:       "127.0.0.1",
		SSHKeyPath: "/opt/nowhere/nonexistent.key",
	}
	_, err = runner.buildClientConfig(serverNoCandidate)
	if err == nil {
		t.Fatal("expected error for non-existent key, got nil")
	}
	if !strings.Contains(err.Error(), "verify that the file exists and, if JobCon is running inside Docker") {
		t.Errorf("expected Docker mount hint in error message, got: %q", err.Error())
	}

	// Case 3: Empty key path
	serverEmpty := &db.Server{
		Name:       "Testserver",
		Host:       "127.0.0.1",
		SSHKeyPath: "",
	}
	_, err = runner.buildClientConfig(serverEmpty)
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
	if !strings.Contains(err.Error(), "no SSH private key configured for server \"Testserver\"") {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}

func TestRunCommand_WritesErrorToOutput(t *testing.T) {
	runner := NewSSHRunner("", 5, 5)
	server := &db.Server{
		Name:       "Server1",
		Host:       "127.0.0.1",
		Port:       22,
		SSHKeyPath: "/path/that/does/not/exist.key",
	}

	var buf strings.Builder
	exitCode, err := runner.RunCommand(context.Background(), server, "echo hello", &buf)
	if err == nil {
		t.Fatal("expected error from RunCommand, got nil")
	}
	if exitCode != -1 {
		t.Errorf("expected exit code -1, got %d", exitCode)
	}
	output := buf.String()
	if !strings.Contains(output, "[JobCon Error] SSH configuration failed:") {
		t.Errorf("expected error header in output, got: %q", output)
	}
	if !strings.Contains(output, "SSH private key file not found") {
		t.Errorf("expected key not found details in output, got: %q", output)
	}
}
