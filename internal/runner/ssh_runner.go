package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"jobcon/internal/db"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type ConnectionTestResult struct {
	Success          bool   `json:"success"`
	LatencyMS        int64  `json:"latency_ms"`
	OSInfo           string `json:"os_info"`
	TalendDirExists  bool   `json:"talend_dir_exists"`
	JobsDirExists    bool   `json:"jobs_dir_exists"`
	ScriptsDirExists bool   `json:"scripts_dir_exists"`
	ScriptsInstalled bool   `json:"scripts_installed"`
	Uptime           string `json:"uptime"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

type ServerSetupResult struct {
	Success        bool     `json:"success"`
	JobsDir        string   `json:"jobs_dir"`
	ScriptsDir     string   `json:"scripts_dir"`
	InstalledFiles []string `json:"installed_files"`
	Message        string   `json:"message"`
	ErrorMessage   string   `json:"error_message,omitempty"`
}

type SSHRunner struct {
	defaultKeyPath string
	timeout        time.Duration
	keepaliveInt   time.Duration
	scriptsDir     string
}

func NewSSHRunner(defaultKeyPath string, timeoutSec, keepaliveSec int, scriptsDir ...string) *SSHRunner {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if keepaliveSec <= 0 {
		keepaliveSec = 30
	}
	sDir := "./scripts"
	if len(scriptsDir) > 0 && scriptsDir[0] != "" {
		sDir = scriptsDir[0]
	}
	return &SSHRunner{
		defaultKeyPath: defaultKeyPath,
		timeout:        time.Duration(timeoutSec) * time.Second,
		keepaliveInt:   time.Duration(keepaliveSec) * time.Second,
		scriptsDir:     sDir,
	}
}

// buildClientConfig prepares SSH ClientConfig with key auth and hostkey verification
func (r *SSHRunner) buildClientConfig(server *db.Server) (*ssh.ClientConfig, error) {
	keyPath := server.SSHKeyPath
	if keyPath == "" {
		keyPath = r.defaultKeyPath
	}

	if keyPath == "" {
		serverName := server.Name
		if serverName == "" {
			serverName = server.Host
		}
		return nil, fmt.Errorf("no SSH private key configured for server %q and no default key path configured", serverName)
	}

	// Expand ~ to user home
	if keyPath == "~" {
		if home := os.Getenv("HOME"); home != "" {
			keyPath = home
		}
	} else if strings.HasPrefix(keyPath, "~/") {
		if home := os.Getenv("HOME"); home != "" {
			keyPath = filepath.Join(home, keyPath[2:])
		}
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			var hint string
			if home := os.Getenv("HOME"); home != "" {
				candidate := filepath.Join(home, ".ssh", filepath.Base(keyPath))
				if _, statErr := os.Stat(candidate); statErr == nil && candidate != keyPath {
					hint = fmt.Sprintf(" (found matching key file at %q; if JobCon is running inside Docker, check whether the server is configured with a host path instead of the container mount path, e.g. '~/.ssh/%s')", candidate, filepath.Base(keyPath))
				}
			}
			if hint == "" {
				hint = " (verify that the file exists and, if JobCon is running inside Docker, that the key or ~/.ssh directory is mounted into the container at this path)"
			}
			return nil, fmt.Errorf("SSH private key file not found at %q%s: %w", keyPath, hint, err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("permission denied reading SSH private key %q (ensure appropriate file permissions e.g. 0600 and container user access): %w", keyPath, err)
		}
		return nil, fmt.Errorf("failed to read private key %q: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		var passphraseErr *ssh.PassphraseMissingError
		if errors.As(err, &passphraseErr) {
			return nil, fmt.Errorf("SSH private key %q is passphrase-protected, which is not supported without an SSH agent: %w", keyPath, err)
		}
		return nil, fmt.Errorf("failed to parse SSH private key %q (ensure it is a valid OpenSSH/PEM RSA or Ed25519 private key): %w", keyPath, err)
	}

	user := server.User
	if user == "" {
		user = "talend"
	}

	return &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For internal enterprise networks
		Timeout:         r.timeout,
	}, nil
}

// TestConnection verifies SSH access and retrieves host diagnostics
func (r *SSHRunner) TestConnection(ctx context.Context, server *db.Server) (*ConnectionTestResult, error) {
	start := time.Now()
	sshConfig, err := r.buildClientConfig(server)
	if err != nil {
		return &ConnectionTestResult{
			Success:      false,
			ErrorMessage: err.Error(),
		}, nil
	}

	addr := net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	conn, err := net.DialTimeout("tcp", addr, r.timeout)
	if err != nil {
		return &ConnectionTestResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("TCP dial failed: %v", err),
		}, nil
	}
	defer conn.Close()

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		return &ConnectionTestResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("SSH handshake failed: %v", err),
		}, nil
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &ConnectionTestResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to open session: %v", err),
		}, nil
	}
	defer session.Close()

	// Probe OS, Uptime, Talend directory, JobsDir, ScriptsDir, and ScriptsInstalled
	jobsDir := server.JobsDir
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	scriptsDir := server.ScriptsDir
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	ctlScript := fmt.Sprintf("%s/jobcon_ctl.sh", strings.TrimRight(scriptsDir, "/"))

	var stdout bytes.Buffer
	session.Stdout = &stdout
	cmd := fmt.Sprintf(`echo "---OS---"; uname -srm; echo "---UPTIME---"; uptime; echo "---TALEND---"; [ -d /opt/talend ] && echo "yes" || echo "no"; echo "---JOBS_DIR---"; [ -d %s ] && echo "yes" || echo "no"; echo "---SCRIPTS_DIR---"; [ -d %s ] && echo "yes" || echo "no"; echo "---SCRIPTS_INSTALLED---"; [ -x %s ] && echo "yes" || echo "no"`, ShellQuote(jobsDir), ShellQuote(scriptsDir), ShellQuote(ctlScript))
	_ = session.Run(cmd)

	latency := time.Since(start).Milliseconds()
	output := stdout.String()

	osInfo := "Linux"
	uptime := "unknown"
	talendExists := false
	jobsDirExists := false
	scriptsDirExists := false
	scriptsInstalled := false

	parts := strings.Split(output, "---")
	for i := 1; i < len(parts); i += 2 {
		header := strings.TrimSpace(parts[i])
		val := ""
		if i+1 < len(parts) {
			val = strings.TrimSpace(parts[i+1])
		}

		switch header {
		case "OS":
			osInfo = val
		case "UPTIME":
			uptime = val
		case "TALEND":
			talendExists = strings.HasPrefix(val, "yes")
		case "JOBS_DIR":
			jobsDirExists = strings.HasPrefix(val, "yes")
		case "SCRIPTS_DIR":
			scriptsDirExists = strings.HasPrefix(val, "yes")
		case "SCRIPTS_INSTALLED":
			scriptsInstalled = strings.HasPrefix(val, "yes")
		}
	}

	return &ConnectionTestResult{
		Success:          true,
		LatencyMS:        latency,
		OSInfo:           osInfo,
		TalendDirExists:  talendExists,
		JobsDirExists:    jobsDirExists,
		ScriptsDirExists: scriptsDirExists,
		ScriptsInstalled: scriptsInstalled,
		Uptime:           uptime,
	}, nil
}

// SetupServer creates the configured jobs and scripts directories and copies execution scripts to target
func (r *SSHRunner) SetupServer(ctx context.Context, server *db.Server) (*ServerSetupResult, error) {
	jobsDir := server.JobsDir
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	scriptsDir := server.ScriptsDir
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}

	result := &ServerSetupResult{
		JobsDir:    jobsDir,
		ScriptsDir: scriptsDir,
	}

	// 1. Read local scripts from configured directory first
	entries, err := os.ReadDir(r.scriptsDir)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("Lokales Skriptverzeichnis %q konnte nicht gelesen werden: %v", r.scriptsDir, err)
		return result, nil
	}

	scriptMap := make(map[string][]byte)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sh") {
			continue
		}
		path := filepath.Join(r.scriptsDir, entry.Name())
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			result.ErrorMessage = fmt.Sprintf("Lokales Skript %q konnte nicht gelesen werden: %v", entry.Name(), readErr)
			return result, nil
		}
		scriptMap[entry.Name()] = content
	}

	if _, hasCtl := scriptMap["jobcon_ctl.sh"]; !hasCtl {
		result.ErrorMessage = fmt.Sprintf("Erforderliches Steuerungsskript 'jobcon_ctl.sh' fehlt im Verzeichnis %q", r.scriptsDir)
		return result, nil
	}

	// 2. Connect via SSH
	sshConfig, err := r.buildClientConfig(server)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("Ungültige SSH-Konfiguration: %v", err)
		return result, nil
	}

	addr := net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	conn, err := net.DialTimeout("tcp", addr, r.timeout)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("TCP-Verbindung fehlgeschlagen: %v", err)
		return result, nil
	}
	defer conn.Close()

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("SSH-Handshake fehlgeschlagen: %v", err)
		return result, nil
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	// 3. Create directories
	sessionMkdir, err := client.NewSession()
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("SSH-Session für mkdir fehlgeschlagen: %v", err)
		return result, nil
	}
	var stderrMkdir bytes.Buffer
	sessionMkdir.Stderr = &stderrMkdir
	mkdirCmd := fmt.Sprintf("mkdir -p %s %s", ShellQuote(jobsDir), ShellQuote(scriptsDir))
	if err := sessionMkdir.Run(mkdirCmd); err != nil {
		sessionMkdir.Close()
		result.ErrorMessage = fmt.Sprintf("Verzeichnisse konnten nicht angelegt werden (%s): %v (%s)", mkdirCmd, err, strings.TrimSpace(stderrMkdir.String()))
		return result, nil
	}
	sessionMkdir.Close()

	// 3. Upload all scripts to target
	var installedFiles []string
	var verifyChecks []string
	cleanScriptsDir := strings.TrimRight(scriptsDir, "/")

	for scriptName, content := range scriptMap {
		targetFile := fmt.Sprintf("%s/%s", cleanScriptsDir, scriptName)
		sessionUpload, err := client.NewSession()
		if err != nil {
			result.ErrorMessage = fmt.Sprintf("SSH-Session für %s fehlgeschlagen: %v", scriptName, err)
			return result, nil
		}
		sessionUpload.Stdin = bytes.NewReader(content)
		var stderrUpload bytes.Buffer
		sessionUpload.Stderr = &stderrUpload
		writeCmd := fmt.Sprintf("cat > %s && chmod 0755 %s", ShellQuote(targetFile), ShellQuote(targetFile))
		if err := sessionUpload.Run(writeCmd); err != nil {
			sessionUpload.Close()
			result.ErrorMessage = fmt.Sprintf("%s konnte nicht übertragen werden: %v (%s)", scriptName, err, strings.TrimSpace(stderrUpload.String()))
			return result, nil
		}
		sessionUpload.Close()
		installedFiles = append(installedFiles, scriptName)
		verifyChecks = append(verifyChecks, fmt.Sprintf("[ -x %s ]", ShellQuote(targetFile)))
	}

	// 4. Verify scripts on target
	if len(verifyChecks) > 0 {
		sessionVerify, err := client.NewSession()
		if err == nil {
			verifyCmd := strings.Join(verifyChecks, " && ")
			_ = sessionVerify.Run(verifyCmd)
			sessionVerify.Close()
		}
	}

	result.Success = true
	result.InstalledFiles = installedFiles
	result.Message = fmt.Sprintf("Verzeichnisse (%s, %s) erfolgreich eingerichtet und Scripte (%s) mit Rechten 0755 installiert.",
		jobsDir, scriptsDir, strings.Join(installedFiles, ", "))
	return result, nil
}

// RunCommand executes a command remotely on the target server, streaming output to writer
func (r *SSHRunner) RunCommand(ctx context.Context, server *db.Server, command string, output io.Writer) (int, error) {
	sshConfig, err := r.buildClientConfig(server)
	if err != nil {
		if output != nil {
			fmt.Fprintf(output, "[JobCon Error] SSH configuration failed: %v\n", err)
		}
		return -1, fmt.Errorf("invalid SSH configuration: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		if output != nil {
			fmt.Fprintf(output, "[JobCon Error] SSH connection to %s (%s@%s) failed: %v\n", addr, sshConfig.User, server.Host, err)
		}
		return -1, fmt.Errorf("failed to dial SSH server %s: %w", addr, err)
	}
	defer client.Close()

	// Keepalive sender
	stopKeepalive := make(chan struct{})
	defer close(stopKeepalive)
	go func() {
		ticker := time.NewTicker(r.keepaliveInt)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _, _ = client.SendRequest("keepalive@openssh.com", true, nil)
			case <-stopKeepalive:
				return
			}
		}
	}()

	session, err := client.NewSession()
	if err != nil {
		if output != nil {
			fmt.Fprintf(output, "[JobCon Error] Failed to create SSH session on %s: %v\n", addr, err)
		}
		return -1, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = output
	session.Stderr = output

	// Direct regex barrier guard on command before executing via SSH
	if !safeSSHCommandRegex.MatchString(command) {
		if output != nil {
			fmt.Fprintf(output, "[JobCon Error] Command rejected: unsafe command pattern: %s\n", command)
		}
		return -1, fmt.Errorf("refusing to execute command with unsafe pattern: %s", command)
	}

	// Start command asynchronously to allow context cancellation
	if err := session.Start(command); err != nil {
		if output != nil {
			fmt.Fprintf(output, "[JobCon Error] Failed to start remote command on %s: %v\n", addr, err)
		}
		return -1, fmt.Errorf("failed to start remote command: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case <-ctx.Done():
		if output != nil {
			fmt.Fprintf(output, "\n[JobCon] Execution aborted by user/context.\n")
		}
		// Graceful abort: send signal or close session
		_ = session.Signal(ssh.SIGTERM)
		// Give process 2 seconds to terminate before closing session
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = session.Close()
		}
		return 130, errors.New("execution aborted by user/context")

	case err := <-done:
		if err != nil {
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				return exitErr.ExitStatus(), nil
			}
			if output != nil {
				fmt.Fprintf(output, "\n[JobCon Error] Remote execution error: %v\n", err)
			}
			return -1, err
		}
		return 0, nil
	}
}
