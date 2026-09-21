package nexus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNexusBrowseTree(t *testing.T) {
	// Mock Nexus server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/search" {
			res := searchResponse{
				Items: []Component{
					{Group: "de.firma.talend", Name: "sync_sap_kunden", Version: "1.0.0"},
					{Group: "de.firma.talend", Name: "sync_sap_kunden", Version: "1.1.0"},
					{Group: "de.firma.talend", Name: "export_billing", Version: "2.0.0"},
					{Group: "com.other", Name: "import_data", Version: "0.5.0"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
			return
		}
		if r.URL.Path == "/service/rest/v1/repositories" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL, "admin", "secret")

	// Test connection
	ok, msg, err := client.TestConnection(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected successful connection, got ok=%v, msg=%s, err=%v", ok, msg, err)
	}

	// Test BrowseTree
	tree, err := client.BrowseTree(context.Background(), "releases", "", "")
	if err != nil {
		t.Fatalf("failed to browse tree: %v", err)
	}

	if len(tree) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(tree))
	}

	// First group: com.other
	if tree[0].Group != "com.other" {
		t.Errorf("expected com.other, got %s", tree[0].Group)
	}
	// Second group: de.firma.talend
	if tree[1].Group != "de.firma.talend" {
		t.Errorf("expected de.firma.talend, got %s", tree[1].Group)
	}
	if len(tree[1].Artifacts) != 2 {
		t.Errorf("expected 2 artifacts in de.firma.talend, got %d", len(tree[1].Artifacts))
	}
	// Check versions for sync_sap_kunden (sorted newest first)
	for _, art := range tree[1].Artifacts {
		if art.ArtifactID == "sync_sap_kunden" {
			if len(art.Versions) != 2 || art.Versions[0] != "1.1.0" || art.Versions[1] != "1.0.0" {
				t.Errorf("unexpected versions order: %v", art.Versions)
			}
		}
	}
}

func TestNexusConnectionAnonymous(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/repositories" {
			// verify that no authorization header is sent
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Errorf("expected no authorization header for anonymous client, got %s", auth)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL, "", "")
	ok, msg, err := client.TestConnection(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected successful anonymous connection, got ok=%v, msg=%s, err=%v", ok, msg, err)
	}
	if msg != "Verbindung erfolgreich (anonymer Zugriff)" {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestNewClientURLNormalization(t *testing.T) {
	c1 := NewClient("http://nexus.intern:8081", "user", "pass")
	if c1.BaseURL != "http://nexus.intern:8081" {
		t.Errorf("expected http://nexus.intern:8081, got %s", c1.BaseURL)
	}

	c2 := NewClient("http://nexus.intern:8081/", "user", "pass")
	if c2.BaseURL != "http://nexus.intern:8081" {
		t.Errorf("expected http://nexus.intern:8081, got %s", c2.BaseURL)
	}

	c3 := NewClient("http://nexus.intern:8081/repository", "user", "pass")
	if c3.BaseURL != "http://nexus.intern:8081" {
		t.Errorf("expected http://nexus.intern:8081, got %s", c3.BaseURL)
	}

	c4 := NewClient("http://nexus.intern:8081/repository/", "user", "pass")
	if c4.BaseURL != "http://nexus.intern:8081" {
		t.Errorf("expected http://nexus.intern:8081, got %s", c4.BaseURL)
	}
}

func TestSanitizeNexusQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"   ", ""},
		{"*customer*", "customer*"},
		{"?sync", "sync"},
		{"***foo", "foo"},
		{"\"quoted\"", "quoted"},
		{"v1.2.0", "1.2.0"},
		{"V2.0.1", "2.0.1"},
		{"version1", "version1"}, // not v followed by digit
		{"com.opitzhome.jobs", "com.opitzhome.jobs"},
	}

	for _, tc := range tests {
		got := sanitizeNexusQuery(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeNexusQuery(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestNexusSearchComponents_WithQuery(t *testing.T) {
	var capturedQ, capturedGroup, capturedRepo, capturedName string

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/search" {
			capturedQ = r.URL.Query().Get("q")
			capturedGroup = r.URL.Query().Get("group")
			capturedRepo = r.URL.Query().Get("repository")
			capturedName = r.URL.Query().Get("name")

			res := searchResponse{
				Items: []Component{
					{Group: "com.opitzhome.jobs", Name: "customer_sync", Version: "1.0.0"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL, "admin", "secret")

	// Search using com.opitzhome.jobs as query
	comps, err := client.SearchComponents(context.Background(), "releases", "", "*com.opitzhome.jobs*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comps) != 1 {
		t.Fatalf("expected 1 component, got %d", len(comps))
	}
	if capturedRepo != "releases" {
		t.Errorf("expected repo releases, got %s", capturedRepo)
	}
	if capturedQ != "com.opitzhome.jobs*" {
		t.Errorf("expected q 'com.opitzhome.jobs*', got %s", capturedQ)
	}
	if capturedGroup != "" {
		t.Errorf("expected group param to be empty, got %s", capturedGroup)
	}
	if capturedName != "" {
		t.Errorf("expected name param to be empty, got %s", capturedName)
	}
}

func TestNexusSearchComponents_Advanced(t *testing.T) {
	var capturedQ, capturedGroup, capturedRepo, capturedName string

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/search" {
			capturedQ = r.URL.Query().Get("q")
			capturedGroup = r.URL.Query().Get("group")
			capturedRepo = r.URL.Query().Get("repository")
			capturedName = r.URL.Query().Get("name")

			res := searchResponse{
				Items: []Component{
					{Group: "de.firma.talend", Name: "sync_sap", Version: "1.0.0"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL, "admin", "secret")

	// Search using group and name
	tree, err := client.BrowseTreeAdvanced(context.Background(), "snapshots", "de.firma.talend", "sync_sap", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree) != 1 || tree[0].Group != "de.firma.talend" {
		t.Fatalf("expected 1 group de.firma.talend, got %+v", tree)
	}
	if capturedRepo != "snapshots" {
		t.Errorf("expected repo 'snapshots', got %s", capturedRepo)
	}
	if capturedGroup != "de.firma.talend*" {
		t.Errorf("expected group 'de.firma.talend*', got %s", capturedGroup)
	}
	if capturedName != "sync_sap*" {
		t.Errorf("expected name 'sync_sap*', got %s", capturedName)
	}
	if capturedQ != "" {
		t.Errorf("expected empty q, got %s", capturedQ)
	}
}

func TestNexusSearchComponents_ErrorParsing(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/search" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`[{"id":"*","message":"Leading wildcards are prohibited"}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL, "admin", "secret")
	_, err := client.SearchComponents(context.Background(), "releases", "", "anything")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectedMsg := "nexus returned HTTP 400: Leading wildcards are prohibited"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestNexusSearchComponents_Pagination(t *testing.T) {
	page := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/search" {
			page++
			w.Header().Set("Content-Type", "application/json")
			if page == 1 {
				tok := "tok123"
				res := searchResponse{
					Items:             []Component{{Group: "g", Name: "a1", Version: "1.0.0"}},
					ContinuationToken: &tok,
				}
				_ = json.NewEncoder(w).Encode(res)
				return
			}
			res := searchResponse{
				Items:             []Component{{Group: "g", Name: "a2", Version: "2.0.0"}},
				ContinuationToken: nil,
			}
			_ = json.NewEncoder(w).Encode(res)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL, "admin", "secret")
	comps, err := client.SearchComponents(context.Background(), "releases", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comps) != 2 {
		t.Fatalf("expected 2 components from 2 pages, got %d", len(comps))
	}
}

func TestLiveNexusBrowseTree(t *testing.T) {
	baseURL := os.Getenv("NEXUS_BASE_URL")
	if baseURL == "" {
		baseURL = "http://omv.localdomain:8081"
	}
	user := os.Getenv("NEXUS_USERNAME")
	if user == "" {
		user = "admin"
	}
	pass := os.Getenv("NEXUS_PASSWORD")
	if pass == "" {
		t.Skip("NEXUS_PASSWORD not set; skipping live test")
	}

	client := NewClient(baseURL, user, pass)
	ok, _, err := client.TestConnection(context.Background())
	if err != nil || !ok {
		t.Skip("Live Nexus not available")
	}

	tree, err := client.BrowseTree(context.Background(), "talend-releases", "", "com.opitzhome.jobs")
	if err != nil {
		t.Fatalf("failed to browse tree with filter 'com.opitzhome.jobs': %v", err)
	}
	if len(tree) != 1 || tree[0].Group != "com.opitzhome.jobs" {
		t.Fatalf("unexpected tree results: %+v", tree)
	}
	t.Logf("Found %d artifacts in group %s", len(tree[0].Artifacts), tree[0].Group)
	for _, a := range tree[0].Artifacts {
		t.Logf(" - %s (%v)", a.ArtifactID, a.Versions)
	}

	// Also verify that a leading wildcard query like "*customer*" doesn't cause 400
	tree2, err := client.BrowseTree(context.Background(), "talend-releases", "", "*customer*")
	if err != nil {
		t.Fatalf("failed to browse tree with wildcard '*customer*': %v", err)
	}
	if len(tree2) == 0 {
		t.Fatalf("expected artifacts for '*customer*', got 0")
	}
	t.Logf("Found %d groups for '*customer*'", len(tree2))
}


