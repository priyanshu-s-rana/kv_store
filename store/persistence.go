package store

import (
	"github.com/priyanshu-s-rana/kv_store/utils"
)

type SnapshotEntry struct {
	Value  []byte
	Expiry int64
}

type SnapshotResponse struct {
	Data map[string]SnapshotEntry
	Err  error
}

func (se *SnapshotEntry) HasExpiry() bool {
	return !(se.Expiry == 0)
}

func (se *SnapshotEntry) IsExpired() bool {
	if se.Expiry == 0 {
		return false
	}
	return utils.AbsoluteTimeNow() >= se.Expiry
}

func (s *Store) capture() (map[string]SnapshotEntry, error) {
	data := make(map[string]SnapshotEntry, len(s.data))
	for k, e := range s.data {
		if e.isExpired() {
			continue
		}
		value := make([]byte, len(e.value))
		copy(value, e.value)
		data[k] = SnapshotEntry{Value: value, Expiry: e.expiry}
	}

	return data, nil
}
