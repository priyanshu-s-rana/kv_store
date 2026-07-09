package persistence

import (
	"context"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

type Journal struct {
	aofs       [2]*AOF
	generation atomic.Uint64
	sequenceID atomic.Uint64
}

func NewJournal(configs [2]*AOFConfig) (*Journal, error) {
	var aofs [2]*AOF
	for i, value := range configs {
		if aof, err := NewAOF(value); err != nil {
			return nil, err
		} else {
			aofs[i] = aof
		}
	}
	return &Journal{
		aofs: aofs,
	}, nil
}

func (j *Journal) current() *AOF {
	return j.aofs[j.generation.Load()&1]
}

func (j *Journal) standby() *AOF {
	return j.aofs[(j.generation.Load()&1)^1]
}

func (j *Journal) Append(name constants.CmdName, args []string) error {
	nextSequenceID := j.sequenceID.Load() + 1
	if err := j.current().Append(name, args, nextSequenceID); err != nil {
		return err
	}

	j.sequenceID.Store(nextSequenceID)
	return nil
}

func (j *Journal) Start(ctx context.Context, wg *sync.WaitGroup) error {
	currentAof := j.current()
	if currentAof.aofConfig.SyncPolicy == constants.SyncEverySec {
		j.everySec(ctx, wg)
	}
	return currentAof.ensureInitialize(j.generation.Load())
}

func (j *Journal) Rotate() error {
	if err := j.current().fsync(); err != nil {
		return err
	}

	nextGen := j.generation.Load() + 1
	if err := j.standby().Initialize(nextGen); err != nil {
		return err
	}

	j.generation.Store(nextGen)
	return nil
}

func (j *Journal) Replay(snapshotFile *snapshotFile, cmd chan<- Command) (bool, error) {
	var aofsMetadata [2]*AOFMetadata
	for i, aof := range j.aofs {
		if _, err := aof.file.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
		if aofMeta, err := aof.readHeader(); err != nil {
			return false, err
		} else {
			aofsMetadata[i] = aofMeta
		}
	}

	if aofsMetadata[0].Generation > aofsMetadata[1].Generation {
		aofsMetadata[0], aofsMetadata[1] = aofsMetadata[1], aofsMetadata[0]
	}

	j.generation.Store(max(aofsMetadata[0].Generation, aofsMetadata[1].Generation))
	latestSequenceID := snapshotFile.LastSequenceID
	replayed := false
	for _, aofMeta := range aofsMetadata {
		if aofMeta.Generation < snapshotFile.Generation {
			continue
		}

		var err error
		isReplayed := false
		if isReplayed, latestSequenceID, err = aofMeta.aof.Replay(cmd, latestSequenceID); err != nil {
			return false, err
		}

		replayed = replayed || isReplayed
	}

	j.sequenceID.Store(latestSequenceID)
	return replayed, nil
}

func (j *Journal) everySec(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	ticker := time.NewTicker(time.Second)
	go func() {
		defer wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				activeAof := j.current()
				if err := activeAof.fsync(); err != nil {
					log.Printf("[Persistence] Error during fsync process in AOF file %s : %v", activeAof.aofConfig.FilePath, err)
				}
			}
		}
	}()
}
