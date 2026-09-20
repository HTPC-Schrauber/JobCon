package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogStorageWriteAndFinalize(t *testing.T) {
	tmpDir := t.TempDir()
	logsDir := filepath.Join(tmpDir, "logs")

	store, err := NewLogStorage(logsDir, true)
	if err != nil {
		t.Fatalf("failed to init log storage: %v", err)
	}

	execID := "exec_12345"
	writer, path, err := store.CreateLogWriter(execID)
	if err != nil {
		t.Fatalf("failed to create log writer: %v", err)
	}

	testContent := "Line 1: Starting Talend job\nLine 2: Processing 500 records\nLine 3: Finished successfully\n"
	if _, err := writer.WriteString(testContent); err != nil {
		t.Fatalf("failed to write log: %v", err)
	}
	writer.Close()

	// Verify uncompressed file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("raw log file does not exist: %v", err)
	}

	// Finalize (should compress to .log.gz)
	finalPath, err := store.FinalizeLog(path)
	if err != nil {
		t.Fatalf("failed to finalize log: %v", err)
	}

	if !strings.HasSuffix(finalPath, ".gz") {
		t.Errorf("expected final path to end in .gz, got %s", finalPath)
	}

	// Read log back
	data, err := store.ReadLog(finalPath)
	if err != nil {
		t.Fatalf("failed to read compressed log: %v", err)
	}

	if string(data) != testContent {
		t.Errorf("expected read content %q, got %q", testContent, string(data))
	}
}
