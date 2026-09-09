package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// Manages file storage directory for chat data.
type Store struct {
	dir string
}

// Creates a chat session store backed by a directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Creates the session store directory if it doesn't exist
func (s *Store) Ensure() error {
	return os.MkdirAll(s.dir, 0755)
}

// Persists a chat session to disk as JSON.
func (s *Store) Save(session *Session) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return helper.AtomicWriteFile(s.sessionPath(session.ID), data, 0644)
}

// Loads and unmarshals a session from a JSON file by ID
func (s *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		return nil, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &session, nil
}

// Loads all chat sessions from disk, sorted by most recent update time.
func (s *Store) LoadAll() ([]*Session, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	sessions := make([]*Session, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}
		sessions = append(sessions, &session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// Removes a session file by ID, ignoring not-found errors
func (s *Store) Delete(id string) error {
	err := os.Remove(s.sessionPath(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Constructs the file path for a session JSON file.
func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}
