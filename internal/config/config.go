// Package config holds per-machine application settings, kept apart from
// the connections file so that file can be shared or synced.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const schemaVersion = 1

// StorageFile is the only storage type today. A database type is planned;
// its connection details will sit alongside Type.
const StorageFile = "file"

type Storage struct {
	Type string `json:"type"`
}

type UI struct {
	SidebarWidth  int  `json:"sidebarWidth"`
	SidebarHidden bool `json:"sidebarHidden"`
}

type Settings struct {
	SchemaVersion int     `json:"schemaVersion"`
	Storage       Storage `json:"storage"`
	UI            UI      `json:"ui"`
}

func defaults() *Settings {
	return &Settings{SchemaVersion: schemaVersion, Storage: Storage{Type: StorageFile}, UI: UI{SidebarWidth: 280}}
}

// Dir returns the per-user config directory: %APPDATA%\myrdp on Windows,
// ~/.config/myrdp on Linux.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "myrdp"), nil
}

// Load reads settings from path, returning defaults if it doesn't exist.
func Load(path string) (*Settings, error) {
	s := defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return defaults(), err
	}
	if s.Storage.Type == "" {
		s.Storage.Type = StorageFile
	}
	return s, nil
}

func (s *Settings) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
