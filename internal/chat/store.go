package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Store struct {
	dir string
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) Ensure() error {
	return os.MkdirAll(s.dir, 0755)
}

func (s *Store) Save(session *Session) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(s.sessionPath(session.ID), data, 0644)
}

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

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}
