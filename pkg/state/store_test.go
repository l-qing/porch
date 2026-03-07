package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewStore(path)

	st := File{
		Version:   1,
		StartedAt: time.Now().UTC(),
		Components: map[string]Component{
			"tektoncd-pipeline": {
				Branch:    "release-1.6",
				SHA:       "abc",
				Namespace: "devops",
				Pipelines: map[string]PipelineState{
					"tp-all-in-one": {Status: "watching", RetryCount: 1},
				},
			},
		},
	}

	if err := s.Save(st); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded.Components["tektoncd-pipeline"].Branch != "release-1.6" {
		t.Fatalf("unexpected loaded data: %+v", loaded)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set")
	}
}

func TestSaveConcurrentNoCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewStore(path)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Save(File{
				Version:   1,
				StartedAt: time.Now().UTC(),
				Components: map[string]Component{
					"c": {
						Branch: "main",
						SHA:    "sha",
						Pipelines: map[string]PipelineState{
							"p": {Status: "watching", RetryCount: i},
						},
					},
				},
			})
		}(i)
	}
	wg.Wait()

	if _, err := s.Load(); err != nil {
		t.Fatalf("Load after concurrent save error: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[0] != '{' {
		t.Fatalf("state file looks corrupted: %q", string(b))
	}
}

func TestSaveCreatesMissingParentDirForLockAndState(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "nested", "state", "state.json")
	s := NewStore(path)

	st := File{
		Version:   1,
		StartedAt: time.Now().UTC(),
		Components: map[string]Component{
			"c": {
				Branch: "main",
				SHA:    "sha",
				Pipelines: map[string]PipelineState{
					"p": {Status: "watching", RetryCount: 0},
				},
			},
		},
	}

	if err := s.Save(st); err != nil {
		t.Fatalf("Save should create missing parent directories, got: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file should exist after Save: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("lock file should exist after Save: %v", err)
	}
}
