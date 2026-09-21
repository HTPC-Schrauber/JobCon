package i18n

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestI18nLoadingAndTranslation(t *testing.T) {
	mgr := NewManager("")

	// Check available languages
	langs := mgr.GetAvailableLanguages()
	if len(langs) < 2 {
		t.Fatalf("expected at least 2 languages (en, de), got %d", len(langs))
	}

	foundEN := false
	foundDE := false
	for _, l := range langs {
		if l.Code == "en" {
			foundEN = true
		}
		if l.Code == "de" {
			foundDE = true
		}
	}
	if !foundEN || !foundDE {
		t.Errorf("expected en and de in languages, foundEN=%v, foundDE=%v", foundEN, foundDE)
	}

	// Test German translations
	if got := mgr.T("de", "nav.jobs"); got != "Jobs" {
		t.Errorf("expected 'Jobs', got %q", got)
	}
	if got := mgr.T("de", "bulk.start"); got != "Starten" {
		t.Errorf("expected 'Starten', got %q", got)
	}

	// Test English translations
	if got := mgr.T("en", "bulk.start"); got != "Run" {
		t.Errorf("expected 'Run', got %q", got)
	}
	if got := mgr.T("en", "dashboard.title"); got != "Talend Jobs" {
		t.Errorf("expected 'Talend Jobs', got %q", got)
	}

	// Test Arguments formatting
	if got := mgr.T("en", "bulk.selected", 3); got != "3 selected" {
		t.Errorf("expected '3 selected', got %q", got)
	}
	if got := mgr.T("de", "bulk.selected", 3); got != "3 ausgewählt" {
		t.Errorf("expected '3 ausgewählt', got %q", got)
	}

	// Test Fallback when key missing in requested language
	if got := mgr.T("fr", "nav.jobs"); got != "Jobs" {
		t.Errorf("expected fallback to en 'Jobs', got %q", got)
	}

	// Test Fallback to key when key missing everywhere
	if got := mgr.T("de", "non.existing.key"); got != "non.existing.key" {
		t.Errorf("expected 'non.existing.key', got %q", got)
	}

	// Test GetJSON produces valid JSON with dot-notation keys
	jsonDe := mgr.GetJSON("de")
	if !strings.Contains(jsonDe, `"bulk.confirm_undeploy"`) {
		t.Errorf("expected jsonDe to contain 'bulk.confirm_undeploy', got: %s", jsonDe)
	}
}

func TestExternalLocalesLoading(t *testing.T) {
	tmpDir := t.TempDir()
	esFile := filepath.Join(tmpDir, "es.json")
	esJSON := `{
		"__meta": {
			"code": "es",
			"name": "Español"
		},
		"nav": {
			"jobs": "Trabajos"
		}
	}`
	if err := os.WriteFile(esFile, []byte(esJSON), 0644); err != nil {
		t.Fatalf("failed to write es.json: %v", err)
	}

	mgr := NewManager(tmpDir)
	langs := mgr.GetAvailableLanguages()
	foundES := false
	for _, l := range langs {
		if l.Code == "es" {
			foundES = true
			if l.Name != "Español" {
				t.Errorf("expected language name 'Español', got %q", l.Name)
			}
		}
	}
	if !foundES {
		t.Errorf("expected dynamically discovered 'es' language")
	}

	if got := mgr.T("es", "nav.jobs"); got != "Trabajos" {
		t.Errorf("expected 'Trabajos', got %q", got)
	}
}
