package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Data captures persisted LLM configuration options.
type Data struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key"`
	UseLLM  bool   `json:"use_llm"`
}

// Store manages reading and writing persistent settings.
type Store struct {
	path string
	mu   sync.RWMutex
}

// NewStore builds a Store below the user's configuration directory.
func NewStore(appID string) (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate config dir: %w", err)
	}

	settingsDir := filepath.Join(dir, appID)
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create settings dir: %w", err)
	}

	path := filepath.Join(settingsDir, "settings.json")
	return &Store{path: path}, nil
}

// Load reads settings from disk. Returns zero Data if the file does not exist.
func (s *Store) Load() (Data, error) {
	if s == nil {
		return Data{}, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	bytes, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Data{}, nil
	}
	if err != nil {
		return Data{}, fmt.Errorf("read settings: %w", err)
	}

	var data Data
	if err := json.Unmarshal(bytes, &data); err != nil {
		return Data{}, fmt.Errorf("decode settings: %w", err)
	}

	return data, nil
}

// Save writes settings to disk atomically.
func (s *Store) Save(data Data) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write temp settings: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("commit settings: %w", err)
	}

	return nil
}
