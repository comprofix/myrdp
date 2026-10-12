// Package store persists the connection tree. The JSON file is the only
// backend today; the interface is shaped so a database backend can be added
// without touching callers.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"myrdp/internal/model"
)

// Store persists a document. Callers mutate the document returned by Load
// and then report each change; a backend may write just that change (a
// database row) or everything (the JSON file).
type Store interface {
	Load() (*model.Document, error)
	// SaveNode persists a new node or a change to a node's own fields.
	SaveNode(n *model.Node) error
	// DeleteNode removes a node and everything below it.
	DeleteNode(id string) error
	// MoveNode persists the node's current parent and position.
	MoveNode(n *model.Node) error
	Close() error
}

const maxBackups = 10

// JSONFile stores the whole document in one file.
type JSONFile struct {
	path string
	doc  *model.Document
}

func NewJSONFile(path string) *JSONFile { return &JSONFile{path: path} }

func (s *JSONFile) Path() string { return s.path }

func (s *JSONFile) Load() (*model.Document, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.doc = model.NewDocument()
		return s.doc, nil
	}
	if err != nil {
		return nil, err
	}
	doc := &model.Document{}
	if err := json.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	if doc.SchemaVersion > model.SchemaVersion {
		return nil, fmt.Errorf("%s was written by a newer version of myrdp (schema %d, this build supports %d)",
			s.path, doc.SchemaVersion, model.SchemaVersion)
	}
	doc.SchemaVersion = model.SchemaVersion
	s.doc = doc
	s.backup(data)
	return doc, nil
}

// backup keeps a copy of the file as it was when the app started.
func (s *JSONFile) backup(data []byte) {
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	name := fmt.Sprintf("connections-%s.json", time.Now().Format("20060102-150405"))
	if os.WriteFile(filepath.Join(dir, name), data, 0o600) != nil {
		return
	}
	old, _ := filepath.Glob(filepath.Join(dir, "connections-*.json"))
	sort.Strings(old)
	for len(old) > maxBackups {
		os.Remove(old[0])
		old = old[1:]
	}
}

func (s *JSONFile) write() error {
	if s.doc == nil {
		return errors.New("store not loaded")
	}
	data, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	// Write beside the target and rename, so a crash never leaves a
	// half-written connections file.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".connections-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(data, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func (s *JSONFile) SaveNode(*model.Node) error { return s.write() }
func (s *JSONFile) DeleteNode(string) error    { return s.write() }
func (s *JSONFile) MoveNode(*model.Node) error { return s.write() }
func (s *JSONFile) Close() error               { return nil }
