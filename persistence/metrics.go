package persistence

import "time"

type PersistenceMetrics interface {
	SetPersistenceEnabled(enabled bool)

	// Recovery
	IncRecoveryRun()
	IncRecoveryFailure()
	IncRecoveredCommands(commands uint64)
	IncRecoveryRebaseline()
	IncRecoveryRebaselineFailure()

	// AOF
	IncAofCommandWritten()
	IncAofCommandWriteFailure()
	IncAofFsync()
	IncAofFsyncFailures()

	// Journal
	IncJournalRotation()
	IncJournalRotationFailure()
	IncJournalReplay()
	IncJournalReplayFailure()
	IncJournalReplaySkippedCommands()
	SetCurrentJournalSizeBytes(bytes uint64)
	SetStandbyJournalSizeBytes(bytes uint64)
	SetCurrentGeneration(generation uint64)
	SetCurrentSequenceID(sequenceID uint64)

	// Checkpoint/Snapshot
	IncCheckpointRun()
	IncCheckpointFailure()
	IncCheckpointSuccess()
	SetCheckpointInProgress(val bool)
	SetLastCheckpointSucceeded(val bool)
	SetSnapshotBytesWritten(bytes uint64)

	// Latency Observers
	ObserveAOFFsyncDuration(duration time.Duration)
	ObserveRecoveryDuration(duration time.Duration)
	ObserveRebaselineDuration(duration time.Duration)
	ObserveSnapshotSaveDuration(duration time.Duration)
	ObserveSnapshotLoadDuration(duration time.Duration)
	ObserveJournalReplayDuration(duration time.Duration)
}
