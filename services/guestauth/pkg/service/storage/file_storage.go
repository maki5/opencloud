package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
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

	// Create temporary file is needed to ensure that if something is wrong during write we will not have
	// a corupted file on disk which can be later treated as a valid file which contains a token record.
	f, err := os.CreateTemp(dir, "tmpguestauth")
	if err != nil {
		return fmt.Errorf("could not create temporary file for %s: %w", rec.ShareIDHash, err)
	}
	defer f.Close()

	if _, writeErr := f.Write(data); writeErr != nil {
		if remErr := os.Remove(f.Name()); remErr != nil {
			return fmt.Errorf("could not cleanup temporary file for %s: %w", rec.ShareIDHash, remErr)
		}
		return fmt.Errorf("could not write temporary file for %s: %w", rec.ShareIDHash, writeErr)
	}

	// just in case there is a simultan write of the identic file(record)
	if synErr := f.Sync(); synErr != nil {
		return fmt.Errorf("could not sync temporary file for %s: %w", rec.ShareIDHash, synErr)
	}

	if renErr := os.Rename(f.Name(), p); renErr != nil {
		if remErr := os.Remove(f.Name()); remErr != nil {
			return fmt.Errorf("rename failed and could not cleanup temporary file for %s: %w", rec.ShareIDHash, remErr)
		}
		return fmt.Errorf("could not rename temporary file to %s: %w", p, renErr)
	}

	return nil
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
