package persistence

import (
	"encoding/gob"
	"os"
	"strings"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

type SnapshotConfig struct {
	FilePath string
	Interval time.Duration
}

type Snapshot struct {
	snapshotConfig *SnapshotConfig
	tempFilePath   string
}

type snapshotFile struct {
	Generation     uint64
	LastSequenceID uint64
	Data           map[string]SnapshotEntry
}

func NewSnapshot(config *SnapshotConfig) *Snapshot {
	tempFilePath := config.FilePath
	if !strings.HasSuffix(tempFilePath, ".tmp") {
		tempFilePath += ".tmp"
	}
	return &Snapshot{
		snapshotConfig: config,
		tempFilePath:   tempFilePath,
	}
}

func (snapshot *Snapshot) SaveToDisk(data map[string]SnapshotEntry, gen uint64, lastSequenceID uint64) (err error) {
	f, err := OpenFile(snapshot.tempFilePath, constants.OpenSnapshot)
	if err != nil {
		return
	}

	defer func() {
		if err != nil {
			_ = os.Remove(snapshot.tempFilePath)
		}
	}()

	if err = gob.NewEncoder(f).Encode(&snapshotFile{Generation: gen, LastSequenceID: lastSequenceID, Data: data}); err != nil {
		_ = f.Close()
		return
	}

	if err = f.Sync(); err != nil {
		_ = f.Close()
		return
	}

	if err = f.Close(); err != nil {
		return
	}

	err = os.Rename(snapshot.tempFilePath, snapshot.snapshotConfig.FilePath)
	return
}

func (snapshot *Snapshot) Load() (*snapshotFile, error) {
	f, err := os.Open(snapshot.snapshotConfig.FilePath)
	if os.IsNotExist(err) {
		return &snapshotFile{
			Data: make(map[string]SnapshotEntry),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	defer f.Close()
	var snapshotFile snapshotFile
	if err := gob.NewDecoder(f).Decode(&snapshotFile); err != nil {
		return nil, err
	}

	return &snapshotFile, nil
}
