package gh

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	fn func(args ...string) ([]byte, []byte, error)
}

func (f fakeRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	return f.fn(args...)
}

func TestBranchSHA(t *testing.T) {
	c := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		if strings.Join(args, " ") != "api repos/TestGroup/repo/commits/main" {
			t.Fatalf("unexpected args: %v", args)
		}
		return []byte(`{"sha":"abc123"}`), nil, nil
	}})

	got, err := c.BranchSHA(context.Background(), "repo", "main")
	if err != nil {
		t.Fatalf("BranchSHA error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("sha = %q, want abc123", got)
	}
}

func TestCheckRuns(t *testing.T) {
	c := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		return []byte(`{"check_runs":[{"id":1,"name":"x","status":"completed","conclusion":"success","details_url":"u","external_id":"eid","output":{"annotations_count":2}}]}`), nil, nil
	}})

	runs, err := c.CheckRuns(context.Background(), "repo", "sha")
	if err != nil {
		t.Fatalf("CheckRuns error: %v", err)
	}
	if len(runs) != 1 || runs[0].ExternalID != "eid" || runs[0].Output.AnnotationsCount != 2 {
		t.Fatalf("unexpected runs: %+v", runs)
	}
}

func TestListBranches(t *testing.T) {
	c := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		if strings.Join(args, " ") != "api --paginate repos/TestGroup/repo/branches?per_page=100 --jq .[].name" {
			t.Fatalf("unexpected args: %v", args)
		}
		return []byte("main\nrelease-1.9\nrelease-1.8\nmain\n"), nil, nil
	}})

	branches, err := c.ListBranches(context.Background(), "repo")
	if err != nil {
		t.Fatalf("ListBranches error: %v", err)
	}
	if len(branches) != 3 {
		t.Fatalf("branches len = %d, want 3", len(branches))
	}
	if branches[0] != "main" || branches[1] != "release-1.8" || branches[2] != "release-1.9" {
		t.Fatalf("unexpected branches: %+v", branches)
	}
}

func TestCheckRunAnnotations(t *testing.T) {
	c := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		if strings.Join(args, " ") != "api repos/TestGroup/repo/check-runs/123/annotations?per_page=100" {
			t.Fatalf("unexpected args: %v", args)
		}
		return []byte(`[{"annotation_level":"failure","title":"x","message":"bad"}]`), nil, nil
	}})

	annotations, err := c.CheckRunAnnotations(context.Background(), "repo", 123)
	if err != nil {
		t.Fatalf("CheckRunAnnotations error: %v", err)
	}
	if len(annotations) != 1 || annotations[0].AnnotationLevel != "failure" {
		t.Fatalf("unexpected annotations: %+v", annotations)
	}
}

func TestCreateCommitCommentErrorIncludesStderr(t *testing.T) {
	c := NewClient("TestGroup", fakeRunner{fn: func(args ...string) ([]byte, []byte, error) {
		return nil, []byte("gh: forbidden"), errors.New("exit status 1")
	}})

	err := c.CreateCommitComment(context.Background(), "repo", "sha", "/test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error missing stderr: %v", err)
	}
}
