package store

import (
	"time"
)

type SnapshotEntry struct {
	Value  []byte
	Expiry time.Time
}

type SnapshotResponse struct {
	Data map[string]SnapshotEntry
	Err  error
}

func (se *SnapshotEntry) HasExpiry() bool {
	return !se.Expiry.IsZero()
}

func (se *SnapshotEntry) IsExpired() bool {
	if se.Expiry.IsZero() {
		return false
	}
	return time.Now().After(se.Expiry)
}

func (s *Store) capture() (map[string]SnapshotEntry, error) {
	exportedSnapshot := make(map[string]SnapshotEntry, len(s.data))
	for k, e := range s.data {
		if e.isExpired() {
			continue
		}
		exportedSnapshot[k] = SnapshotEntry{
			Value:  e.value,
			Expiry: e.expiry,
		}
	}

	return exportedSnapshot, nil
}
