package watcher

import (
	"errors"
	"testing"

	pipestatus "porch/pkg/pipeline"
)

func TestDeriveStatusFromProbe(t *testing.T) {
	status, n := DeriveStatusFromProbe(nil, ProbeResult{Status: pipestatus.StatusSucceeded}, 0, 5)
	if status != pipestatus.StatusSucceeded || n != 0 {
		t.Fatalf("unexpected succeeded mapping: %s %d", status, n)
	}

	status, n = DeriveStatusFromProbe(nil, ProbeResult{Status: pipestatus.StatusFailed}, 0, 5)
	if status != pipestatus.StatusFailed || n != 0 {
		t.Fatalf("unexpected failed mapping: %s %d", status, n)
	}

	status, n = DeriveStatusFromProbe(nil, ProbeResult{Status: pipestatus.StatusRunning}, 0, 5)
	if status != pipestatus.StatusRunning || n != 0 {
		t.Fatalf("unexpected running mapping: %s %d", status, n)
	}

	status, n = DeriveStatusFromProbe(errors.New("x"), ProbeResult{}, 4, 5)
	if status != pipestatus.StatusQueryErr || n != 5 {
		t.Fatalf("unexpected query_error mapping: %s %d", status, n)
	}
}
