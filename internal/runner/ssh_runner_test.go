package runner

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"jobcon/internal/db"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
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

func TestHostKeyCallback_TOFUAndMismatch(t *testing.T) {
	// Generate two test key pairs (ed25519)
	pub1, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	sshKey1, err := ssh.NewPublicKey(pub1)
	if err != nil {
		t.Fatalf("failed to create ssh public key: %v", err)
	}

	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	sshKey2, err := ssh.NewPublicKey(pub2)
	if err != nil {
		t.Fatalf("failed to create ssh public key: %v", err)
	}

	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	server := &db.Server{
		ID:         "srv-tofu-1",
		Name:       "TOFU Test Server",
		Host:       "192.168.1.100",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/key",
	}
	if err := database.CreateServer(server); err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	r := NewSSHRunner("/tmp/key", 5, 5)
	r.SetDB(database)

	cb := r.buildHostKeyCallback(server)

	// 1. First connection: TOFU should record and accept key1
	dummyAddr, _ := net.ResolveTCPAddr("tcp", "192.168.1.100:22")
	if err := cb("192.168.1.100:22", dummyAddr, sshKey1); err != nil {
		t.Fatalf("expected TOFU to accept key1, got err: %v", err)
	}

	if server.HostKey == "" {
		t.Fatal("expected server.HostKey to be populated by TOFU")
	}
	if server.HostKeyFingerprint == "" {
		t.Fatal("expected server.HostKeyFingerprint to be populated by TOFU")
	}

	// Verify key was persisted to DB
	fromDB, err := database.GetServer("srv-tofu-1")
	if err != nil {
		t.Fatalf("failed to get server from db: %v", err)
	}
	if fromDB.HostKey != server.HostKey {
		t.Errorf("expected DB host key %q, got %q", server.HostKey, fromDB.HostKey)
	}

	// 2. Second connection with SAME key should succeed
	if err := cb("192.168.1.100:22", dummyAddr, sshKey1); err != nil {
		t.Fatalf("expected callback to accept matching key, got err: %v", err)
	}

	// 3. Connection with DIFFERENT key (key2) should fail with mismatch error
	err = cb("192.168.1.100:22", dummyAddr, sshKey2)
	if err == nil {
		t.Fatal("expected error on host key mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "Host-Key-Abweichung") {
		t.Errorf("expected mismatch error message, got: %v", err)
	}

	// 4. Reset host key in DB and server struct
	if err := database.ResetServerHostKey("srv-tofu-1"); err != nil {
		t.Fatalf("failed to reset host key: %v", err)
	}
	server.HostKey = ""
	server.HostKeyFingerprint = ""

	// 5. Connection with key2 should now succeed via TOFU and record key2
	if err := cb("192.168.1.100:22", dummyAddr, sshKey2); err != nil {
		t.Fatalf("expected TOFU to accept key2 after reset, got err: %v", err)
	}

	fromDB2, err := database.GetServer("srv-tofu-1")
	if err != nil {
		t.Fatalf("failed to get server from db: %v", err)
	}
	expectedKey2 := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshKey2)))
	if fromDB2.HostKey != expectedKey2 {
		t.Errorf("expected new DB host key %q, got %q", expectedKey2, fromDB2.HostKey)
	}
}

func TestRunCommand_RejectionSanitized(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	go func() {
		nConn, err := listener.Accept()
		if err != nil {
			return
		}
		defer nConn.Close()
		sConn, chans, reqs, err := ssh.NewServerConn(nConn, serverConfig)
		if err != nil {
			return
		}
		defer sConn.Close()
		go ssh.DiscardRequests(reqs)
		for newChannel := range chans {
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go func(in <-chan *ssh.Request) {
				for req := range in {
					_ = req.Reply(true, nil)
				}
			}(requests)
			_ = channel
		}
	}()

	tmpDir := t.TempDir()
	clientKeyPath := filepath.Join(tmpDir, "client_id_ed25519")
	keyPem, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	_ = os.WriteFile(clientKeyPath, pem.EncodeToMemory(keyPem), 0600)

	host, portStr, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	server := &db.Server{
		Host:       host,
		Port:       port,
		User:       "testuser",
		SSHKeyPath: clientKeyPath,
	}

	runner := NewSSHRunner("", 5, 5)
	var output strings.Builder
	unsafeCmd := "jobcon_ctl.sh run --nexus-pass TopSecretPassword123 ; rm -rf /"
	exitCode, err := runner.RunCommand(context.Background(), server, unsafeCmd, &output)
	if err == nil {
		t.Fatal("expected error for unsafe command, got nil")
	}
	if exitCode != -1 {
		t.Errorf("expected exit code -1, got %d", exitCode)
	}

	// Verify that secret is NOT leaked into error or output
	if strings.Contains(err.Error(), "TopSecretPassword123") {
		t.Errorf("secret leaked into error: %v", err)
	}
	if strings.Contains(output.String(), "TopSecretPassword123") {
		t.Errorf("secret leaked into output: %s", output.String())
	}
}

