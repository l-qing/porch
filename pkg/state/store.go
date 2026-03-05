package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() (File, error) {
	var st File

	// Use a file lock for read path as well so readers never observe
	// partially written files during concurrent save.
	lock, err := s.lock()
	if err != nil {
		return st, err
	}
	defer unlockAndClose(lock)

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, fmt.Errorf("state file does not exist: %w", err)
		}
		return st, fmt.Errorf("read state file: %w", err)
	}

	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("decode state file: %w", err)
	}
	return st, nil
}

func (s *Store) Save(st File) error {
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlockAndClose(lock)

	st.UpdatedAt = time.Now().UTC()
	if st.Version == 0 {
		st.Version = 1
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state file: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	// Write-then-rename keeps state updates atomic on local filesystems.
	tmp, err := os.CreateTemp(dir, ".porch-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename temp state file: %w", err)
	}
	return nil
}

func (s *Store) lock() (*os.File, error) {
	lockPath := s.path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	// Flock gives process-level mutual exclusion across concurrent porch runs.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock state file: %w", err)
	}
	return f, nil
}

func unlockAndClose(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
