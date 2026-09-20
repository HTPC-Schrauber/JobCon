package storage

import (
	"compress/gzip"
	"fmt"
	"io"
	"jobcon/internal/db"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type LogStorage struct {
	logsDir           string
	compressCompleted bool
	mu                sync.Mutex
}

func NewLogStorage(logsDir string, compressCompleted bool) (*LogStorage, error) {
	if err := os.MkdirAll(logsDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}
	return &LogStorage{
		logsDir:           logsDir,
		compressCompleted: compressCompleted,
	}, nil
}

// LogFilePath returns the expected raw log file path for an execution
func (s *LogStorage) LogFilePath(executionID string) string {
	return filepath.Join(s.logsDir, executionID+".log")
}

// CompressedLogFilePath returns the gzipped path
func (s *LogStorage) CompressedLogFilePath(executionID string) string {
	return filepath.Join(s.logsDir, executionID+".log.gz")
}

// CreateLogWriter creates and opens a new raw log file for writing
func (s *LogStorage) CreateLogWriter(executionID string) (*os.File, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.LogFilePath(executionID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create log file: %w", err)
	}
	return f, path, nil
}

// FinalizeLog compresses the log file if configured, returning the final stored path
func (s *LogStorage) FinalizeLog(rawPath string) (string, error) {
	if !s.compressCompleted {
		return rawPath, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	gzPath := rawPath + ".gz"
	srcFile, err := os.Open(rawPath)
	if err != nil {
		return rawPath, err
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(gzPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err != nil {
		return rawPath, err
	}
	defer destFile.Close()

	gzWriter := gzip.NewWriter(destFile)
	if _, err := io.Copy(gzWriter, srcFile); err != nil {
		_ = gzWriter.Close()
		return rawPath, err
	}
	if err := gzWriter.Close(); err != nil {
		return rawPath, err
	}

	srcFile.Close()
	_ = os.Remove(rawPath)
	return gzPath, nil
}

// ReadLog reads the log content whether it is compressed or raw
func (s *LogStorage) ReadLog(logPath string) ([]byte, error) {
	// If path doesn't exist, check for .gz or without .gz
	targetPath := logPath
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		if strings.HasSuffix(targetPath, ".gz") {
			uncompressed := strings.TrimSuffix(targetPath, ".gz")
			if _, err2 := os.Stat(uncompressed); err2 == nil {
				targetPath = uncompressed
			}
		} else {
			compressed := targetPath + ".gz"
			if _, err2 := os.Stat(compressed); err2 == nil {
				targetPath = compressed
			}
		}
	}

	f, err := os.Open(targetPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if strings.HasSuffix(targetPath, ".gz") {
		gzReader, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gzReader.Close()
		return io.ReadAll(gzReader)
	}

	return io.ReadAll(f)
}

// CleanRetention removes log files and marks records as purged for old executions
func (s *LogStorage) CleanRetention(database *db.DB, jobID string, retentionRuns int) {
	if retentionRuns <= 0 {
		return
	}

	oldExecs, err := database.GetOldExecutionsForRetention(jobID, retentionRuns)
	if err != nil {
		log.Printf("[Retention] Error fetching old executions for job %s: %v", jobID, err)
		return
	}

	for _, exec := range oldExecs {
		if exec.LogPath != "" {
			_ = os.Remove(exec.LogPath)
			// Also attempt to remove .gz or non-.gz variant
			_ = os.Remove(exec.LogPath + ".gz")
			_ = os.Remove(strings.TrimSuffix(exec.LogPath, ".gz"))
		}
		if err := database.MarkExecutionPurged(exec.ID); err != nil {
			log.Printf("[Retention] Error marking execution %s as purged: %v", exec.ID, err)
		} else {
			log.Printf("[Retention] Purged old execution %s for job %s", exec.ID, jobID)
		}
	}
}

// CleanAllRetention executes retention cleanup across all jobs
func (s *LogStorage) CleanAllRetention(database *db.DB) int {
	jobs, err := database.ListJobs()
	if err != nil {
		log.Printf("[Retention] Failed to list jobs for retention cleanup: %v", err)
		return 0
	}

	purgedCount := 0
	for _, j := range jobs {
		oldExecs, err := database.GetOldExecutionsForRetention(j.ID, j.RetentionRuns)
		if err != nil {
			continue
		}
		for _, exec := range oldExecs {
			if exec.LogPath != "" {
				_ = os.Remove(exec.LogPath)
				_ = os.Remove(exec.LogPath + ".gz")
				_ = os.Remove(strings.TrimSuffix(exec.LogPath, ".gz"))
			}
			if err := database.MarkExecutionPurged(exec.ID); err == nil {
				purgedCount++
			}
		}
	}
	return purgedCount
}
