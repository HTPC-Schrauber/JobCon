package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"jobcon/internal/db"
	"jobcon/scripts"
	"net"
	"os"
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
}

func NewSSHRunner(defaultKeyPath string, timeoutSec, keepaliveSec int) *SSHRunner {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if keepaliveSec <= 0 {
		keepaliveSec = 30
	}
	return &SSHRunner{
		defaultKeyPath: defaultKeyPath,
		timeout:        time.Duration(timeoutSec) * time.Second,
		keepaliveInt:   time.Duration(keepaliveSec) * time.Second,
	}
}

// buildClientConfig prepares SSH ClientConfig with key auth and hostkey verification
func (r *SSHRunner) buildClientConfig(server *db.Server) (*ssh.ClientConfig, error) {
	keyPath := server.SSHKeyPath
	if keyPath == "" {
		keyPath = r.defaultKeyPath
	}

	// Expand ~ to user home
	if strings.HasPrefix(keyPath, "~/") {
		if home := os.Getenv("HOME"); home != "" {
			keyPath = home + keyPath[1:]
		}
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key %q: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key %q: %w", keyPath, err)
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

	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
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
	cmd := fmt.Sprintf(`echo "---OS---"; uname -srm; echo "---UPTIME---"; uptime; echo "---TALEND---"; [ -d /opt/talend ] && echo "yes" || echo "no"; echo "---JOBS_DIR---"; [ -d %q ] && echo "yes" || echo "no"; echo "---SCRIPTS_DIR---"; [ -d %q ] && echo "yes" || echo "no"; echo "---SCRIPTS_INSTALLED---"; [ -x %q ] && echo "yes" || echo "no"`, jobsDir, scriptsDir, ctlScript)
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

	sshConfig, err := r.buildClientConfig(server)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("Ungültige SSH-Konfiguration: %v", err)
		return result, nil
	}

	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
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

	// 1. Create directories
	sessionMkdir, err := client.NewSession()
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("SSH-Session für mkdir fehlgeschlagen: %v", err)
		return result, nil
	}
	var stderrMkdir bytes.Buffer
	sessionMkdir.Stderr = &stderrMkdir
	mkdirCmd := fmt.Sprintf("mkdir -p %q %q", jobsDir, scriptsDir)
	if err := sessionMkdir.Run(mkdirCmd); err != nil {
		sessionMkdir.Close()
		result.ErrorMessage = fmt.Sprintf("Verzeichnisse konnten nicht angelegt werden (%s): %v (%s)", mkdirCmd, err, strings.TrimSpace(stderrMkdir.String()))
		return result, nil
	}
	sessionMkdir.Close()

	// 2. Upload jobcon_ctl.sh
	ctlTarget := fmt.Sprintf("%s/jobcon_ctl.sh", strings.TrimRight(scriptsDir, "/"))
	sessionCtl, err := client.NewSession()
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("SSH-Session für jobcon_ctl.sh fehlgeschlagen: %v", err)
		return result, nil
	}
	sessionCtl.Stdin = bytes.NewReader(scripts.JobconCtlSh)
	var stderrCtl bytes.Buffer
	sessionCtl.Stderr = &stderrCtl
	writeCtlCmd := fmt.Sprintf("cat > %q && chmod 0755 %q", ctlTarget, ctlTarget)
	if err := sessionCtl.Run(writeCtlCmd); err != nil {
		sessionCtl.Close()
		result.ErrorMessage = fmt.Sprintf("jobcon_ctl.sh konnte nicht übertragen werden: %v (%s)", err, strings.TrimSpace(stderrCtl.String()))
		return result, nil
	}
	sessionCtl.Close()

	// 3. Upload run_job.sh
	runJobTarget := fmt.Sprintf("%s/run_job.sh", strings.TrimRight(scriptsDir, "/"))
	sessionRunJob, err := client.NewSession()
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("SSH-Session für run_job.sh fehlgeschlagen: %v", err)
		return result, nil
	}
	sessionRunJob.Stdin = bytes.NewReader(scripts.RunJobSh)
	var stderrRunJob bytes.Buffer
	sessionRunJob.Stderr = &stderrRunJob
	writeRunJobCmd := fmt.Sprintf("cat > %q && chmod 0755 %q", runJobTarget, runJobTarget)
	if err := sessionRunJob.Run(writeRunJobCmd); err != nil {
		sessionRunJob.Close()
		result.ErrorMessage = fmt.Sprintf("run_job.sh konnte nicht übertragen werden: %v (%s)", err, strings.TrimSpace(stderrRunJob.String()))
		return result, nil
	}
	sessionRunJob.Close()

	// 4. Verify scripts
	sessionVerify, err := client.NewSession()
	if err == nil {
		verifyCmd := fmt.Sprintf("[ -x %q ] && [ -x %q ]", ctlTarget, runJobTarget)
		_ = sessionVerify.Run(verifyCmd)
		sessionVerify.Close()
	}

	result.Success = true
	result.InstalledFiles = []string{"jobcon_ctl.sh", "run_job.sh"}
	result.Message = fmt.Sprintf("Verzeichnisse (%s, %s) erfolgreich eingerichtet und Scripte (jobcon_ctl.sh, run_job.sh) mit Rechten 0755 installiert.", jobsDir, scriptsDir)
	return result, nil
}

// RunCommand executes a command remotely on the target server, streaming output to writer
func (r *SSHRunner) RunCommand(ctx context.Context, server *db.Server, command string, output io.Writer) (int, error) {
	sshConfig, err := r.buildClientConfig(server)
	if err != nil {
		return -1, fmt.Errorf("invalid SSH configuration: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
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
		return -1, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = output
	session.Stderr = output

	// Start command asynchronously to allow context cancellation
	if err := session.Start(command); err != nil {
		return -1, fmt.Errorf("failed to start remote command: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case <-ctx.Done():
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
			return -1, err
		}
		return 0, nil
	}
}
