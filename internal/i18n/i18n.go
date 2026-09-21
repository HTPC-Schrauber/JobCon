package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var embeddedLocalesFS embed.FS

type LanguageInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Manager struct {
	mu           sync.RWMutex
	translations map[string]map[string]string // langCode -> (flattened key -> text)
	langMeta     map[string]LanguageInfo      // langCode -> LanguageInfo
	rawJSON      map[string]string            // langCode -> raw JSON string
	defaultLang  string
}

var (
	defaultManager *Manager
	once           sync.Once
)

func GetDefaultManager() *Manager {
	once.Do(func() {
		defaultManager = NewManager("")
	})
	return defaultManager
}

func NewManager(externalLocalesDir string) *Manager {
	m := &Manager{
		translations: make(map[string]map[string]string),
		langMeta:     make(map[string]LanguageInfo),
		rawJSON:      make(map[string]string),
		defaultLang:  "en",
	}

	m.loadEmbedded()
	if externalLocalesDir != "" {
		m.loadExternal(externalLocalesDir)
	}

	return m
}

func (m *Manager) SetDefaultLanguage(lang string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang != "" {
		m.defaultLang = lang
	}
}

func (m *Manager) loadEmbedded() {
	entries, err := embeddedLocalesFS.ReadDir("locales")
	if err != nil {
		log.Printf("[i18n] Error reading embedded locales: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := embeddedLocalesFS.ReadFile("locales/" + entry.Name())
		if err != nil {
			log.Printf("[i18n] Error reading locale %s: %v", entry.Name(), err)
			continue
		}

		m.parseLocaleFile(entry.Name(), data)
	}
}

func (m *Manager) loadExternal(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			log.Printf("[i18n] Error reading external locale %s: %v", entry.Name(), err)
			continue
		}

		m.parseLocaleFile(entry.Name(), data)
	}
}

func (m *Manager) parseLocaleFile(filename string, data []byte) {
	var rawMap map[string]any
	if err := json.Unmarshal(data, &rawMap); err != nil {
		log.Printf("[i18n] Invalid JSON in locale file %s: %v", filename, err)
		return
	}

	code := strings.TrimSuffix(filename, ".json")
	name := strings.ToUpper(code)

	if meta, ok := rawMap["__meta"].(map[string]any); ok {
		if c, ok := meta["code"].(string); ok && c != "" {
			code = c
		}
		if n, ok := meta["name"].(string); ok && n != "" {
			name = n
		}
	}

	flattened := make(map[string]string)
	flatten("", rawMap, flattened)

	m.mu.Lock()
	m.translations[code] = flattened
	m.langMeta[code] = LanguageInfo{Code: code, Name: name}
	m.rawJSON[code] = string(data)
	m.mu.Unlock()
}

func flatten(prefix string, src map[string]any, dst map[string]string) {
	for k, v := range src {
		if k == "__meta" {
			continue
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		switch val := v.(type) {
		case string:
			dst[key] = val
		case map[string]any:
			flatten(key, val, dst)
		case float64:
			dst[key] = fmt.Sprintf("%.0f", val)
		case bool:
			dst[key] = fmt.Sprintf("%v", val)
		}
	}
}

func (m *Manager) GetAvailableLanguages() []LanguageInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []LanguageInfo
	for _, info := range m.langMeta {
		list = append(list, info)
	}

	sort.Slice(list, func(i, j int) bool {
		// Put defaultLang (en) first
		if list[i].Code == m.defaultLang {
			return true
		}
		if list[j].Code == m.defaultLang {
			return false
		}
		return list[i].Name < list[j].Name
	})

	return list
}

func (m *Manager) T(lang, key string, args ...any) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		lang = m.defaultLang
	}

	var text string
	var found bool

	// 1. Try requested language
	if dict, exists := m.translations[lang]; exists {
		text, found = dict[key]
	}

	// 2. Try default language (en) if not found
	if !found && lang != m.defaultLang {
		if dict, exists := m.translations[m.defaultLang]; exists {
			text, found = dict[key]
		}
	}

	// 3. Fallback to key itself
	if !found {
		text = key
	}

	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

func (m *Manager) GetJSON(lang string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lang = strings.ToLower(strings.TrimSpace(lang))
	if dict, ok := m.translations[lang]; ok {
		if data, err := json.Marshal(dict); err == nil {
			return string(data)
		}
	}
	if dict, ok := m.translations[m.defaultLang]; ok {
		if data, err := json.Marshal(dict); err == nil {
			return string(data)
		}
	}
	return "{}"
}

// Global convenience functions
func T(lang, key string, args ...any) string {
	return GetDefaultManager().T(lang, key, args...)
}

func GetAvailableLanguages() []LanguageInfo {
	return GetDefaultManager().GetAvailableLanguages()
}

type Localizer struct {
	lang string
	m    *Manager
}

func (m *Manager) GetLocalizer(lang string) *Localizer {
	return &Localizer{lang: lang, m: m}
}

func (l *Localizer) T(key string, args ...any) string {
	if l == nil || l.m == nil {
		return key
	}
	return l.m.T(l.lang, key, args...)
}

