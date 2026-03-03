package watcher

import (
	"errors"
	"testing"
)

func TestDeriveStatusFromProbe(t *testing.T) {
	status, n := DeriveStatusFromProbe(nil, ProbeResult{Status: "succeeded"}, 0, 5)
	if status != "succeeded" || n != 0 {
		t.Fatalf("unexpected succeeded mapping: %s %d", status, n)
	}

	status, n = DeriveStatusFromProbe(nil, ProbeResult{Status: "failed"}, 0, 5)
	if status != "failed" || n != 0 {
		t.Fatalf("unexpected failed mapping: %s %d", status, n)
	}

	status, n = DeriveStatusFromProbe(nil, ProbeResult{Status: "running"}, 0, 5)
	if status != "running" || n != 0 {
		t.Fatalf("unexpected running mapping: %s %d", status, n)
	}

	status, n = DeriveStatusFromProbe(errors.New("x"), ProbeResult{}, 4, 5)
	if status != "query_error" || n != 5 {
		t.Fatalf("unexpected query_error mapping: %s %d", status, n)
	}
}
