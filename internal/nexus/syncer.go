package nexus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"log"
	"strconv"
	"sync"
	"time"
)

var (
	ErrSyncInProgress = errors.New("nexus sync is already in progress")
)

type SyncStatus struct {
	Status             string     `json:"status"` // "idle", "running", "error"
	IsSyncing          bool       `json:"is_syncing"`
	LastSyncedAt       *time.Time `json:"last_synced_at,omitempty"`
	LastSyncDurationMS int64      `json:"last_sync_duration_ms"`
	ItemCount          int        `json:"item_count"`
	LastError          string     `json:"last_error,omitempty"`
	IntervalMinutes    int        `json:"interval_minutes"`
}

type Syncer struct {
	db        *db.DB
	cfg       *config.Config
	getClient func() *Client

	mu        sync.RWMutex
	isRunning bool
	status    SyncStatus
}

func NewSyncer(database *db.DB, cfg *config.Config, getClient func() *Client) *Syncer {
	s := &Syncer{
		db:        database,
		cfg:       cfg,
		getClient: getClient,
		status: SyncStatus{
			Status:          "idle",
			IntervalMinutes: 60,
		},
	}

	// Restore state from settings table
	if lastAtStr, err := database.GetSetting("nexus_sync_last_at", ""); err == nil && lastAtStr != "" {
		if t, err := time.Parse(time.RFC3339, lastAtStr); err == nil {
			s.status.LastSyncedAt = &t
		}
	}
	if countStr, err := database.GetSetting("nexus_sync_item_count", ""); err == nil && countStr != "" {
		if cnt, err := strconv.Atoi(countStr); err == nil {
			s.status.ItemCount = cnt
		}
	}
	if lastErr, err := database.GetSetting("nexus_sync_last_error", ""); err == nil {
		s.status.LastError = lastErr
	}
	if intvStr, err := database.GetSetting("nexus_sync_interval_minutes", "60"); err == nil && intvStr != "" {
		if intv, err := strconv.Atoi(intvStr); err == nil {
			s.status.IntervalMinutes = intv
		}
	}

	return s
}

// GetIntervalMinutes returns the currently configured sync interval
func (s *Syncer) GetIntervalMinutes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intvStr, err := s.db.GetSetting("nexus_sync_interval_minutes", "60")
	if err == nil && intvStr != "" {
		if intv, err := strconv.Atoi(intvStr); err == nil {
			return intv
		}
	}
	return s.status.IntervalMinutes
}

// SetIntervalMinutes updates the sync interval in DB and memory
func (s *Syncer) SetIntervalMinutes(minutes int) error {
	s.mu.Lock()
	s.status.IntervalMinutes = minutes
	s.mu.Unlock()
	return s.db.SetSetting("nexus_sync_interval_minutes", strconv.Itoa(minutes))
}

// GetStatus returns the current sync status
func (s *Syncer) GetStatus() SyncStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := s.status
	res.IntervalMinutes = s.GetIntervalMinutes()
	if s.isRunning {
		res.Status = "running"
	}
	res.IsSyncing = s.isRunning || res.Status == "running"
	// Refresh item count from DB if idle
	if !s.isRunning {
		if count, err := s.db.GetNexusArtifactCount(); err == nil {
			res.ItemCount = count
		}
	}
	return res
}

func (s *Syncer) getConfiguredRepositories() []string {
	reposJSON, err := s.db.GetSetting("nexus_repositories", "")
	if err == nil && reposJSON != "" {
		var repos []config.NexusRepository
		if err := json.Unmarshal([]byte(reposJSON), &repos); err == nil && len(repos) > 0 {
			var ids []string
			for _, r := range repos {
				if r.ID != "" {
					ids = append(ids, r.ID)
				}
			}
			if len(ids) > 0 {
				return ids
			}
		}
	}

	var ids []string
	for _, r := range s.cfg.Nexus.Repositories {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		ids = []string{"releases"}
	}
	return ids
}

// Sync performs a full synchronization of all configured repositories from Nexus to the local DB
func (s *Syncer) Sync(ctx context.Context) error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return ErrSyncInProgress
	}
	s.isRunning = true
	s.status.Status = "running"
	s.status.LastError = ""
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isRunning = false
		s.mu.Unlock()
	}()

	startTime := time.Now()
	client := s.getClient()
	if client == nil || client.BaseURL == "" {
		err := errors.New("nexus base_url is not configured")
		s.finishWithError(err)
		return err
	}

	repos := s.getConfiguredRepositories()
	log.Printf("[NexusSyncer] Starting synchronization for repositories: %v", repos)

	for _, repo := range repos {
		components, err := client.FetchAllComponents(ctx, repo)
		if err != nil {
			log.Printf("[NexusSyncer] Error fetching components for repository %q: %v", repo, err)
			s.finishWithError(fmt.Errorf("repository %s: %w", repo, err))
			return err
		}

		var syncedComps []db.SyncedComponent
		for _, comp := range components {
			syncedComps = append(syncedComps, db.SyncedComponent{
				Group:   comp.Group,
				Name:    comp.Name,
				Version: comp.Version,
			})
		}

		if err := s.db.ReplaceNexusArtifacts(repo, syncedComps); err != nil {
			log.Printf("[NexusSyncer] Error saving artifacts for repository %q into database: %v", repo, err)
			s.finishWithError(fmt.Errorf("db save %s: %w", repo, err))
			return err
		}
		log.Printf("[NexusSyncer] Synced %d artifacts for repository %q", len(syncedComps), repo)
	}

	duration := time.Since(startTime).Milliseconds()
	totalCount, _ := s.db.GetNexusArtifactCount()
	now := time.Now()

	s.mu.Lock()
	s.status.Status = "idle"
	s.status.LastSyncedAt = &now
	s.status.LastSyncDurationMS = duration
	s.status.ItemCount = totalCount
	s.status.LastError = ""
	s.mu.Unlock()

	_ = s.db.SetSetting("nexus_sync_last_at", now.Format(time.RFC3339))
	_ = s.db.SetSetting("nexus_sync_item_count", strconv.Itoa(totalCount))
	_ = s.db.SetSetting("nexus_sync_last_error", "")

	log.Printf("[NexusSyncer] Sync completed successfully in %dms. Total artifacts cached: %d", duration, totalCount)
	return nil
}

func (s *Syncer) finishWithError(err error) {
	s.mu.Lock()
	s.status.Status = "error"
	s.status.LastError = err.Error()
	s.mu.Unlock()

	_ = s.db.SetSetting("nexus_sync_last_error", err.Error())
}

// StartScheduler starts the periodic background sync ticker
func (s *Syncer) StartScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			interval := s.GetIntervalMinutes()
			if interval <= 0 {
				continue
			}

			s.mu.RLock()
			lastSynced := s.status.LastSyncedAt
			running := s.isRunning
			s.mu.RUnlock()

			if running {
				continue
			}

			shouldSync := false
			if lastSynced == nil {
				// Initial sync if table is empty
				cnt, _ := s.db.GetNexusArtifactCount()
				if cnt == 0 {
					shouldSync = true
				}
			} else if time.Since(*lastSynced) >= time.Duration(interval)*time.Minute {
				shouldSync = true
			}

			if shouldSync {
				client := s.getClient()
				if client != nil && client.BaseURL != "" {
					go func() {
						syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
						defer cancel()
						if err := s.Sync(syncCtx); err != nil {
							log.Printf("[NexusSyncer] Scheduled sync error: %v", err)
						}
					}()
				}
			}
		}
	}
}
