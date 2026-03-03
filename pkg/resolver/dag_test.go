package resolver

import (
	"strings"
	"testing"

	"porch/pkg/config"
)

func TestDAGIsReady(t *testing.T) {
	components := []config.LoadedComponent{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	deps := map[string]config.Depends{
		"b": {DependsOn: []string{"a"}},
		"c": {DependsOn: []string{"b"}},
	}
	dag, err := New(components, deps)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	s := map[string]bool{"a": true, "b": false, "c": false}
	if !dag.IsReady("b", s) {
		t.Fatal("b should be ready when a succeeded")
	}
	if dag.IsReady("c", s) {
		t.Fatal("c should not be ready while b not succeeded")
	}
}

func TestDAGCycle(t *testing.T) {
	components := []config.LoadedComponent{{Name: "a"}, {Name: "b"}}
	deps := map[string]config.Depends{
		"a": {DependsOn: []string{"b"}},
		"b": {DependsOn: []string{"a"}},
	}
	_, err := New(components, deps)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("unexpected error: %v", err)
	}
}
