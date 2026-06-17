package store

import (
	"context"
	"encoding/gob"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

type snapshotEntry struct {
	Value  []byte
	Expiry time.Time
}

func (se *snapshotEntry) hasExpiry() bool {
	return !se.Expiry.IsZero()
}
func (se *snapshotEntry) isExpired() bool {
	if se.Expiry.IsZero() {
		return false
	}
	return time.Now().After(se.Expiry)
}

type SnapshotResponse struct {
	data map[string]snapshotEntry
	err  error
}

// StartSnapshotting saves the store to disk at every interval until ctx is cancelled.
// The caller is responsible for cancelling ctx to stop the goroutine and release the ticker.
func (s *Store) StartSnapshotting(ctx context.Context, path string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.SaveToDisk(path); err != nil {
					log.Printf(constants.SNAPSHOT_FAILED, err)
				} else {
					log.Printf(constants.SNAPSHOT_SAVED, path)
				}
			}
		}
	}()
}

func (s *Store) SaveToDisk(path string) error {

	s.cmdChan <- Command{Name: constants.Snapshot}

	resp := <-s.snapResp
	if resp.err != nil {
		return resp.err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tempPath := path + ".tmp"
	f, err := os.Create(tempPath)
	if err != nil {
		return err
	}

	if err := gob.NewEncoder(f).Encode(resp.data); err != nil {
		f.Close()
		os.Remove(tempPath)
		return err
	}

	f.Close()
	return os.Rename(tempPath, path)
}

func (s *Store) LoadFromDisk(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		log.Printf(constants.SNAPSHOT_NOT_FOUND, path)
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	snapshot := make(map[string]snapshotEntry)

	if err := gob.NewDecoder(f).Decode(&snapshot); err != nil {
		return err
	}

	for key, se := range snapshot {
		if se.hasExpiry() && se.isExpired() {
			continue
		}
		s.data[key] = &entry{
			value:  se.Value,
			expiry: se.Expiry,
		}
		if se.hasExpiry() {
			s.ttls.Push(ttlItem{key: key, expiresAt: se.Expiry})
		}
	}

	log.Printf(constants.SNAPSHOT_LOADED, len(snapshot), path)
	return nil
}
