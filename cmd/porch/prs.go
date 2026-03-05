package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parsePRNumbers parses comma-separated pull request numbers and keeps input order.
// Duplicates are removed to avoid repeated retries/comments on the same PR.
func parsePRNumbers(raw string) ([]int, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, nil
	}
	parts := strings.Split(text, ",")
	out := make([]int, 0, len(parts))
	seen := map[int]struct{}{}
	for _, part := range parts {
		current := strings.TrimSpace(part)
		if current == "" {
			return nil, fmt.Errorf("invalid --prs value %q: empty pr number", raw)
		}
		n, err := strconv.Atoi(current)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid --prs value %q: %q is not a positive integer", raw, current)
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}
