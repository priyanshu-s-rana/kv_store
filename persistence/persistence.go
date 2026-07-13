package persistence

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/priyanshu-s-rana/kv_store/store"
)

type (
	Command       = models.Command
	Response      = models.Response
	SnapshotEntry = store.SnapshotEntry
)

type CheckpointState struct {
	InProgress    atomic.Bool
	LastSucceeded atomic.Bool
}

type Persistence struct {
	journal         *Journal
	snapshot        *Snapshot
	cmdChan         chan<- Command
	snapshotFile    *snapshotFile
	checkpointState *CheckpointState
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	metrics         PersistenceMetrics
}

func New(ctx context.Context, cancel context.CancelFunc, cmdChan chan<- Command, journalConfig [2]*AOFConfig, snapshotConfig *SnapshotConfig, metrics PersistenceMetrics) (*Persistence, error) {
	log.Printf("[Persistence] Journal Configs 1: %+v, 2: %+v", journalConfig[0], journalConfig[1])
	snapshot := NewSnapshot(snapshotConfig, metrics)
	journal, err := NewJournal(journalConfig, metrics)
	if err != nil {
		return nil, err
	}
	checkpointState := &CheckpointState{}
	checkpointState.LastSucceeded.Store(true)

	metrics.SetPersistenceEnabled(true)
	metrics.SetLastCheckpointSucceeded(true)
	return &Persistence{
		journal:         journal,
		snapshot:        snapshot,
		cmdChan:         cmdChan,
		checkpointState: checkpointState,
		ctx:             ctx,
		cancel:          cancel,
		metrics:         metrics,
	}, nil
}

func (p *Persistence) Start() error {
	if err := p.journal.Start(p.ctx, &p.wg); err != nil {
		return err
	}
	if p.snapshot.snapshotConfig.Interval > 0 {
		p.startSnapshotting()
	}
	return nil
}

func (p *Persistence) Close() {
	p.cancel()
	p.wg.Wait()                // finish any existing checkpoint
	p.triggerFinalCheckpoint() // create latest checkpoint
	p.wg.Wait()                // wait for it
}

func (p *Persistence) LoadFromDisk() error {
	start := time.Now()
	defer func() {
		p.metrics.ObserveSnapshotLoadDuration(time.Since(start))
	}()
	snapshotFile, err := p.snapshot.Load()
	if err != nil {
		return err
	}

	p.snapshotFile = snapshotFile
	keysRecovered := 0
	keysNotRecovered := 0
	for key, value := range p.snapshotFile.Data {
		if value.IsExpired() {
			continue
		}

		args := []string{key, string(value.Value)}
		if value.HasExpiry() {
			remainingSecs := int(time.Until(value.Expiry).Seconds())
			args = append(args, []string{"EX", strconv.Itoa(remainingSecs)}...)
		}

		if err := sendCommandToEventLoop(p.cmdChan, constants.Set, args); err == nil {
			keysRecovered++
		} else {
			log.Printf("[Persistence] LoadFromDisk error: %v", err)
			keysNotRecovered++
		}
	}

	p.metrics.IncRecoveredCommands(uint64(keysRecovered))
	log.Printf("[Persistence] Keys recovered by Snapshot: %d , not able to recover: %d", keysRecovered, keysNotRecovered)
	return nil
}

func (p *Persistence) ReplayJournal() (bool, error) {
	start := time.Now()
	p.metrics.IncJournalReplay()
	defer func() {
		p.metrics.ObserveJournalReplayDuration(time.Since(start))
	}()

	replayed, err := p.journal.Replay(p.snapshotFile, p.cmdChan)
	if err != nil {
		p.metrics.IncJournalReplayFailure()
		return false, err
	}
	return replayed, nil
}

func (p *Persistence) Recovery() (err error) {
	start := time.Now()
	p.metrics.IncRecoveryRun()
	defer func() {
		if err != nil {
			p.metrics.IncRecoveryFailure()
		}
		p.metrics.ObserveRecoveryDuration(time.Since(start))
	}()

	if err = p.LoadFromDisk(); err != nil {
		return
	}

	replayed, err := p.ReplayJournal()
	if err != nil {
		return
	}

	log.Printf("[Persistance] Recovery Complete: generation = %d, sequence_id = %d", p.journal.generation.Load(), p.journal.sequenceID.Load())

	if replayed {
		p.metrics.IncRecoveryRebaseline()
		log.Printf("[Persistance] Creating fresh baseline snapshot...")
		if err := sendCommandToEventLoop(p.cmdChan, constants.Rebaseline, nil); err != nil {
			p.metrics.IncRecoveryRebaselineFailure()
			return err
		}
	}

	return
}

// ================== Interface Functions ===================

func (p *Persistence) Append(name constants.CmdName, args []string) error {
	if err := p.journal.Append(name, args); err != nil {
		p.metrics.IncAofCommandWriteFailure()
		return err
	}
	p.metrics.IncAofCommandWritten()
	return nil
}

func (p *Persistence) Checkpoint(snapshot map[string]SnapshotEntry) error {
	if p.checkpointState.InProgress.Load() {
		return fmt.Errorf(constants.CHECKPOINT_IN_PROGRESS)
	}

	p.metrics.IncCheckpointRun()
	p.setCheckpointInProgress(true)
	sealedGen := p.journal.generation.Load()
	lastSequenceID := p.journal.sequenceID.Load()
	if p.checkpointState.LastSucceeded.Load() {
		if err := p.journal.Rotate(); err != nil {
			p.markCheckpointFailed()
			return err
		}
	}

	log.Printf("[Persistance] Starting Checkpoint: generation = %d, sequence_id = %d", sealedGen, lastSequenceID)

	p.wg.Go(func() {
		if err := p.saveSnapshot(snapshot, sealedGen, lastSequenceID); err != nil {
			p.markCheckpointFailed()
			return
		}

		p.markCheckpointSuccess()
	})
	return nil
}

func (p *Persistence) Rebaseline(snapshot map[string]SnapshotEntry) (err error) {
	start := time.Now()
	defer func() {
		p.metrics.ObserveRebaselineDuration(time.Since(start))
	}()

	currentGen := p.journal.generation.Load()
	if err = p.saveSnapshot(snapshot, currentGen, p.journal.sequenceID.Load()); err != nil {
		return
	}

	nextGen := p.journal.generation.Load() + 1
	if err = p.journal.standby().Initialize(nextGen); err != nil {
		return
	}

	p.metrics.SetStandbyJournalSizeBytes(p.journal.current().size.Load())
	p.journal.generation.Store(nextGen)
	p.metrics.SetCurrentGeneration(p.journal.generation.Load())
	return nil
}
