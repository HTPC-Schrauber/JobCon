package nexus

import (
	"context"
	"encoding/json"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSyncer_Sync(t *testing.T) {
	// Setup mock Nexus server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/components" {
			repo := r.URL.Query().Get("repository")
			token := r.URL.Query().Get("continuationToken")

			if repo == "releases" {
				if token == "" {
					nextToken := "page2"
					res := searchResponse{
						Items: []Component{
							{Group: "com.opitzhome.jobs", Name: "invoice_export", Version: "1.0.0"},
							{Group: "com.opitzhome.jobs", Name: "invoice_export", Version: "1.1.0"},
						},
						ContinuationToken: &nextToken,
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(res)
					return
				} else if token == "page2" {
					res := searchResponse{
						Items: []Component{
							{Group: "com.opitzhome.jobs", Name: "inventory_update", Version: "2.0.0"},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(res)
					return
				}
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	// Setup temp db
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	cfg := &config.Config{
		Nexus: config.NexusConfig{
			BaseURL: mockServer.URL,
			Repositories: []config.NexusRepository{
				{ID: "releases", Label: "Releases"},
			},
		},
	}

	syncer := NewSyncer(database, cfg, func() *Client {
		return NewClient(mockServer.URL, "user", "pass")
	})

	// Initial status
	st := syncer.GetStatus()
	if st.Status != "idle" || st.ItemCount != 0 {
		t.Errorf("unexpected initial status: %+v", st)
	}

	// Run sync
	err = syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify status
	st = syncer.GetStatus()
	if st.Status != "idle" {
		t.Errorf("expected idle status, got %s", st.Status)
	}
	if st.ItemCount != 3 {
		t.Errorf("expected 3 items synced, got %d", st.ItemCount)
	}
	if st.LastSyncedAt == nil {
		t.Errorf("expected LastSyncedAt to be non-nil")
	}

	// Verify data in DB
	nodes, err := database.SearchNexusArtifacts("releases", "", "", "")
	if err != nil {
		t.Fatalf("SearchNexusArtifacts failed: %v", err)
	}
	if len(nodes) != 1 || len(nodes[0].Artifacts) != 2 {
		t.Fatalf("unexpected search results: %+v", nodes)
	}

	// Test interval update
	if err := syncer.SetIntervalMinutes(15); err != nil {
		t.Fatalf("SetIntervalMinutes failed: %v", err)
	}
	if syncer.GetIntervalMinutes() != 15 {
		t.Errorf("expected interval 15, got %d", syncer.GetIntervalMinutes())
	}
}
