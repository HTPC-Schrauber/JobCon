package scripts_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func createZip(t *testing.T, jobName string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(fmt.Sprintf("%s/%s_run.sh", jobName, jobName))
	if err != nil {
		t.Fatalf("failed to create zip entry: %v", err)
	}
	_, _ = f.Write([]byte("#!/bin/sh\necho OK\n"))
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip: %v", err)
	}
	return buf.Bytes()
}

func TestJobconCtlGenerationSymlinks(t *testing.T) {
	scriptPath, err := filepath.Abs("jobcon_ctl.sh")
	if err != nil {
		t.Fatalf("failed to find jobcon_ctl.sh: %v", err)
	}

	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "talend")
	jobName := "test_job"
	jobRoot := filepath.Join(baseDir, "jobs", jobName)
	releasesDir := filepath.Join(jobRoot, "releases")

	zipContent := createZip(t, jobName)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipContent)
	}))
	defer ts.Close()

	runDeploy := func(version string, keep int) {
		cmd := exec.Command("/bin/bash", scriptPath, "deploy",
			"--job", jobName,
			"--version", version,
			"--nexus-url", ts.URL+"/job.zip",
			"--base-dir", baseDir,
			"--keep", fmt.Sprintf("%d", keep),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("deploy failed for version %s: %v\nOutput: %s", version, err, string(out))
		}
	}

	readTarget := func(linkName string) string {
		linkPath := filepath.Join(jobRoot, linkName)
		target, err := os.Readlink(linkPath)
		if err != nil {
			return ""
		}
		return target
	}

	// 1. Deploy v1.0.0 with keep 3
	runDeploy("1.0.0", 3)
	if got := readTarget("current"); got != "releases/1.0.0" {
		t.Errorf("expected current -> releases/1.0.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "" {
		t.Errorf("expected current-1 not to exist yet, got %q", got)
	}

	// 2. Deploy v1.1.0 with keep 3
	runDeploy("1.1.0", 3)
	if got := readTarget("current"); got != "releases/1.1.0" {
		t.Errorf("expected current -> releases/1.1.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "releases/1.0.0" {
		t.Errorf("expected current-1 -> releases/1.0.0, got %q", got)
	}

	// 3. Deploy v1.2.0 with keep 3
	runDeploy("1.2.0", 3)
	if got := readTarget("current"); got != "releases/1.2.0" {
		t.Errorf("expected current -> releases/1.2.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "releases/1.1.0" {
		t.Errorf("expected current-1 -> releases/1.1.0, got %q", got)
	}
	if got := readTarget("current-2"); got != "releases/1.0.0" {
		t.Errorf("expected current-2 -> releases/1.0.0, got %q", got)
	}

	// Verify all 3 releases exist in releases/
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		if _, err := os.Stat(filepath.Join(releasesDir, v)); err != nil {
			t.Errorf("expected release dir %s to exist: %v", v, err)
		}
	}

	// 4. Deploy v1.3.0 with keep 3 -> 1.0.0 should be purged!
	runDeploy("1.3.0", 3)
	if got := readTarget("current"); got != "releases/1.3.0" {
		t.Errorf("expected current -> releases/1.3.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "releases/1.2.0" {
		t.Errorf("expected current-1 -> releases/1.2.0, got %q", got)
	}
	if got := readTarget("current-2"); got != "releases/1.1.0" {
		t.Errorf("expected current-2 -> releases/1.1.0, got %q", got)
	}

	// 1.0.0 must be removed
	if _, err := os.Stat(filepath.Join(releasesDir, "1.0.0")); !os.IsNotExist(err) {
		t.Errorf("expected release dir 1.0.0 to be purged, but it still exists")
	}

	// 5. Re-deploying current version (v1.3.0) -> should not duplicate or change links
	runDeploy("1.3.0", 3)
	if got := readTarget("current"); got != "releases/1.3.0" {
		t.Errorf("expected current -> releases/1.3.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "releases/1.2.0" {
		t.Errorf("expected current-1 -> releases/1.2.0, got %q", got)
	}
	if got := readTarget("current-2"); got != "releases/1.1.0" {
		t.Errorf("expected current-2 -> releases/1.1.0, got %q", got)
	}

	// 6. Rollback to v1.1.0 (which was current-2)
	runDeploy("1.1.0", 3)
	if got := readTarget("current"); got != "releases/1.1.0" {
		t.Errorf("expected current -> releases/1.1.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "releases/1.3.0" {
		t.Errorf("expected current-1 -> releases/1.3.0, got %q", got)
	}
	if got := readTarget("current-2"); got != "releases/1.2.0" {
		t.Errorf("expected current-2 -> releases/1.2.0, got %q", got)
	}
	// All three (1.1.0, 1.2.0, 1.3.0) must still exist
	for _, v := range []string{"1.1.0", "1.2.0", "1.3.0"} {
		if _, err := os.Stat(filepath.Join(releasesDir, v)); err != nil {
			t.Errorf("expected release dir %s to exist after rollback: %v", v, err)
		}
	}

	// 7. Deploy v2.0.0 with keep 1 -> only current should exist, all others purged!
	runDeploy("2.0.0", 1)
	if got := readTarget("current"); got != "releases/2.0.0" {
		t.Errorf("expected current -> releases/2.0.0, got %q", got)
	}
	if got := readTarget("current-1"); got != "" {
		t.Errorf("expected current-1 to be removed when keep=1, got %q", got)
	}
	if got := readTarget("current-2"); got != "" {
		t.Errorf("expected current-2 to be removed when keep=1, got %q", got)
	}

	// Only 2.0.0 must exist in releases/
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		t.Fatalf("failed to read releases dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "2.0.0" {
		t.Errorf("expected only release 2.0.0 in releases dir, found %+v", entries)
	}
}
