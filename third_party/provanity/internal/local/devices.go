package local

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseDeviceIDs(raw string) ([]int, bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "all" {
		return nil, false, nil
	}
	if raw == "select" {
		return nil, true, nil
	}

	parts := strings.Split(raw, ",")
	ids := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, fmt.Errorf("empty device id in %q", raw)
		}
		id, err := strconv.Atoi(part)
		if err != nil {
			return nil, false, fmt.Errorf("parse device id %q: %w", part, err)
		}
		if id < 0 {
			return nil, false, fmt.Errorf("device id cannot be negative: %d", id)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	return ids, false, nil
}
