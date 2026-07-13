package persistence

import (
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/store"
)

// noopPersistenceMetrics is a no-op PersistenceMetrics used to build real
// AOF/Journal/Snapshot/Persistence instances in tests without needing a
// Prometheus registry (importing the metrics package here would cycle back
// to persistence).
type noopPersistenceMetrics struct{}

func (noopPersistenceMetrics) SetPersistenceEnabled(bool) {}

func (noopPersistenceMetrics) IncRecoveryRun()               {}
func (noopPersistenceMetrics) IncRecoveryFailure()           {}
func (noopPersistenceMetrics) IncRecoveredCommands(uint64)   {}
func (noopPersistenceMetrics) IncRecoveryRebaseline()        {}
func (noopPersistenceMetrics) IncRecoveryRebaselineFailure() {}

func (noopPersistenceMetrics) IncAofCommandWritten()      {}
func (noopPersistenceMetrics) IncAofCommandWriteFailure() {}
func (noopPersistenceMetrics) IncAofFsync()               {}
func (noopPersistenceMetrics) IncAofFsyncFailures()       {}

func (noopPersistenceMetrics) IncJournalRotation()               {}
func (noopPersistenceMetrics) IncJournalRotationFailure()        {}
func (noopPersistenceMetrics) IncJournalReplay()                 {}
func (noopPersistenceMetrics) IncJournalReplayFailure()          {}
func (noopPersistenceMetrics) IncJournalReplaySkippedCommands()  {}
func (noopPersistenceMetrics) SetCurrentJournalSizeBytes(uint64) {}
func (noopPersistenceMetrics) SetStandbyJournalSizeBytes(uint64) {}
func (noopPersistenceMetrics) SetCurrentGeneration(uint64)       {}
func (noopPersistenceMetrics) SetCurrentSequenceID(uint64)       {}

func (noopPersistenceMetrics) IncCheckpointRun()               {}
func (noopPersistenceMetrics) IncCheckpointFailure()           {}
func (noopPersistenceMetrics) IncCheckpointSuccess()           {}
func (noopPersistenceMetrics) SetCheckpointInProgress(bool)    {}
func (noopPersistenceMetrics) SetLastCheckpointSucceeded(bool) {}
func (noopPersistenceMetrics) SetSnapshotBytesWritten(uint64)  {}

func (noopPersistenceMetrics) ObserveAOFFsyncDuration(time.Duration)      {}
func (noopPersistenceMetrics) ObserveRecoveryDuration(time.Duration)      {}
func (noopPersistenceMetrics) ObserveRebaselineDuration(time.Duration)    {}
func (noopPersistenceMetrics) ObserveSnapshotSaveDuration(time.Duration)  {}
func (noopPersistenceMetrics) ObserveSnapshotLoadDuration(time.Duration)  {}
func (noopPersistenceMetrics) ObserveJournalReplayDuration(time.Duration) {}

// noopStoreMetrics is a no-op store.StoreMetrics used only to satisfy
// store.New's signature when persistence tests wire up a real Store.
type noopStoreMetrics struct{}

func (noopStoreMetrics) IncCommandsExecuted(constants.CmdName)                   {}
func (noopStoreMetrics) IncCommandFailures(constants.CmdName)                    {}
func (noopStoreMetrics) ObserveCommandDuration(constants.CmdName, time.Duration) {}

func (noopStoreMetrics) SetCurrentMemoryBytes(int64)  {}
func (noopStoreMetrics) SetPeakMemoryBytes(int64)     {}
func (noopStoreMetrics) SetMaxMemoryBytes(int64)      {}
func (noopStoreMetrics) SetMemoryUtilization(float32) {}
func (noopStoreMetrics) SetKeyCount(int64)            {}
func (noopStoreMetrics) SetKeyBytes(int64)            {}
func (noopStoreMetrics) SetValueBytes(int64)          {}
func (noopStoreMetrics) SetTTLBytes(int64)            {}
func (noopStoreMetrics) SetLRUBytes(int64)            {}
func (noopStoreMetrics) SetPubSubBytes(int64)         {}

func (noopStoreMetrics) IncExpiredKeys()                        {}
func (noopStoreMetrics) ObserveTTLExpiryDuration(time.Duration) {}

func (noopStoreMetrics) SetActiveTopics(int64)                {}
func (noopStoreMetrics) SetActiveSubscribers(int64)           {}
func (noopStoreMetrics) IncMessagesPublished()                {}
func (noopStoreMetrics) ObservePublishDuration(time.Duration) {}

var (
	_ PersistenceMetrics = noopPersistenceMetrics{}
	_ store.StoreMetrics = noopStoreMetrics{}
)
