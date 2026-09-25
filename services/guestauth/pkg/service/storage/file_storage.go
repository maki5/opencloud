package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/renameio/v2"
)

func NewFileStorage(root string) *FileStorage {
	return &FileStorage{
		root: root,
	}
}

type FileStorage struct {
	root string
	mu   sync.Mutex
}

const dirPerm = 0700
const filePerm = 0600

func (s *FileStorage) Add(rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.add(rec)
}

// Get returns the record for the given share id hash.
func (s *FileStorage) Get(shareIDHash string) (Record, error) {
	return s.get(shareIDHash)
}

func (s *FileStorage) Remove(shareIDHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := s.path(shareIDHash)
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}

	if err := os.Remove(p); err != nil {
		return err
	}

	return nil
}

func (s *FileStorage) Redeem(shareIDHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.get(shareIDHash)
	if err != nil {
		return err
	}

	if rec.Redeemed {
		return ErrAlreadyRedeemed
	}

	rec.Redeemed = true
	return s.add(rec)
}

func (s *FileStorage) add(rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	p := s.path(rec.ShareIDHash)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("could not create directory %s: %w", dir, err)
	}

	return renameio.WriteFile(p, data, filePerm)
}

func (s *FileStorage) get(shareIDHash string) (Record, error) {
	data, err := os.ReadFile(s.path(shareIDHash))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Record{}, ErrNotFound
		}
		return Record{}, err
	}

	rec := Record{}
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, err
	}

	return rec, nil
}

func (s *FileStorage) path(shareIDHash string) string {
	return filepath.Join(s.root, shareIDHash[:2], shareIDHash[2:4], shareIDHash[4:]+".json")
}
