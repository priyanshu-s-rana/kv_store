package metrics

import (
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/prometheus/client_golang/prometheus"
)

type PrometheusPersistenceMetrics struct {
	// Gauges
	persistenceEnabled prometheus.Gauge

	// Recovery
	recoveryRuns               prometheus.Counter
	recoveryFailures           prometheus.Counter
	recoveredCommands          prometheus.Counter
	recoveryRebaselines        prometheus.Counter
	recoveryRebaselineFailures prometheus.Counter
	recoveryDuration           prometheus.Histogram

	// AOF
	aofCommandWritten       prometheus.Counter
	aofCommandWriteFailures prometheus.Counter
	aofFsyncs               prometheus.Counter
	aofFsyncFailures        prometheus.Counter
	aofFsyncDuration        prometheus.Histogram

	// Journal
	journalRotations             prometheus.Counter
	journalRotationFailures      prometheus.Counter
	journalReplays               prometheus.Counter
	journalReplayFailures        prometheus.Counter
	journalReplaySkippedCommands prometheus.Counter
	currentJournalSize           prometheus.Gauge
	standbyJournalSize           prometheus.Gauge
	currentGeneration            prometheus.Gauge
	currentSequenceID            prometheus.Gauge
	journalReplayDuration        prometheus.Histogram

	// Checkpoint/Snapshot
	// rebaselineDuration lives here (not under Recovery) because Rebaseline()
	// is a general checkpoint-family operation: the store's REBASELINE command
	// invokes it directly, and post-recovery auto-rebaseline is just one caller
	// among others.
	checkpointRuns       prometheus.Counter
	checkpointFailures   prometheus.Counter
	checkpointSuccesses  prometheus.Counter
	checkpointInProgress prometheus.Gauge
	checkpointSucceeded  prometheus.Gauge
	snapshotBytesWritten prometheus.Gauge
	rebaselineDuration   prometheus.Histogram
	snapshotSaveDuration prometheus.Histogram
	snapshotLoadDuration prometheus.Histogram
}

func NewPrometheusPersistenceMetrics() *PrometheusPersistenceMetrics {
	return &PrometheusPersistenceMetrics{
		persistenceEnabled: newGauge(constants.MetricsSubsystemPersistence, "enabled", "Whether persistence is enabled (1) or not (0)."),

		recoveryRuns:               newCounter(constants.MetricsSubsystemPersistence, "recovery_runs_total", "Total number of recovery runs."),
		recoveryFailures:           newCounter(constants.MetricsSubsystemPersistence, "recovery_failures_total", "Total number of recovery failures."),
		recoveredCommands:          newCounter(constants.MetricsSubsystemPersistence, "recovered_commands_total", "Total number of commands restored during recovery, combining keys loaded from the snapshot and commands replayed from the journal."),
		recoveryRebaselines:        newCounter(constants.MetricsSubsystemPersistence, "recovery_rebaselines_total", "Total number of rebaselines triggered during recovery."),
		recoveryRebaselineFailures: newCounter(constants.MetricsSubsystemPersistence, "recovery_rebaseline_failures_total", "Total number of rebaseline failures during recovery."),
		recoveryDuration:           newHistogram(constants.MetricsSubsystemPersistence, "recovery_duration_seconds", "Duration of a full recovery run, in seconds.", ioDurationBuckets),

		aofCommandWritten:       newCounter(constants.MetricsSubsystemPersistence, "aof_commands_written_total", "Total number of commands written to the AOF."),
		aofCommandWriteFailures: newCounter(constants.MetricsSubsystemPersistence, "aof_command_write_failures_total", "Total number of command write failures to the AOF."),
		aofFsyncs:               newCounter(constants.MetricsSubsystemPersistence, "aof_fsyncs_total", "Total number of AOF fsyncs."),
		aofFsyncFailures:        newCounter(constants.MetricsSubsystemPersistence, "aof_fsync_failures_total", "Total number of AOF fsync failures."),
		aofFsyncDuration:        newHistogram(constants.MetricsSubsystemPersistence, "aof_fsync_duration_seconds", "Duration of an AOF fsync, in seconds.", ioDurationBuckets),

		journalRotations:             newCounter(constants.MetricsSubsystemPersistence, "journal_rotations_total", "Total number of journal rotations."),
		journalRotationFailures:      newCounter(constants.MetricsSubsystemPersistence, "journal_rotation_failures_total", "Total number of journal rotation failures."),
		journalReplays:               newCounter(constants.MetricsSubsystemPersistence, "journal_replays_total", "Total number of journal replays."),
		journalReplayFailures:        newCounter(constants.MetricsSubsystemPersistence, "journal_replay_failures_total", "Total number of journal replay failures."),
		journalReplaySkippedCommands: newCounter(constants.MetricsSubsystemPersistence, "journal_replay_skipped_commands_total", "Total number of commands skipped during journal replay."),
		currentJournalSize:           newGauge(constants.MetricsSubsystemPersistence, "current_journal_size_bytes", "Current size of the active journal, in bytes."),
		standbyJournalSize:           newGauge(constants.MetricsSubsystemPersistence, "standby_journal_size_bytes", "Current size of the standby journal, in bytes."),
		currentGeneration:            newGauge(constants.MetricsSubsystemPersistence, "current_journal_generation", "Generation of the currently active journal."),
		currentSequenceID:            newGauge(constants.MetricsSubsystemPersistence, "current_sequence_id", "Sequence ID of the last command appended to the journal."),
		journalReplayDuration:        newHistogram(constants.MetricsSubsystemPersistence, "journal_replay_duration_seconds", "Duration of a journal replay, in seconds.", ioDurationBuckets),

		checkpointRuns:       newCounter(constants.MetricsSubsystemPersistence, "checkpoint_runs_total", "Total number of checkpoints triggered."),
		checkpointFailures:   newCounter(constants.MetricsSubsystemPersistence, "checkpoint_failures_total", "Total number of checkpoints that failed."),
		checkpointSuccesses:  newCounter(constants.MetricsSubsystemPersistence, "checkpoint_successes_total", "Total number of checkpoints that succeeded."),
		checkpointInProgress: newGauge(constants.MetricsSubsystemPersistence, "checkpoint_in_progress", "Whether a checkpoint is currently in progress (1) or not (0)."),
		checkpointSucceeded:  newGauge(constants.MetricsSubsystemPersistence, "checkpoint_last_successful", "Whether the last checkpoint succeeded (1) or failed (0)."),
		snapshotBytesWritten: newGauge(constants.MetricsSubsystemPersistence, "snapshot_bytes_written", "Size of the most recently written snapshot, in bytes."),
		rebaselineDuration:   newHistogram(constants.MetricsSubsystemPersistence, "rebaseline_duration_seconds", "Duration of a rebaseline operation, in seconds.", ioDurationBuckets),
		snapshotSaveDuration: newHistogram(constants.MetricsSubsystemPersistence, "snapshot_save_duration_seconds", "Duration of saving a snapshot to disk, in seconds.", ioDurationBuckets),
		snapshotLoadDuration: newHistogram(constants.MetricsSubsystemPersistence, "snapshot_load_duration_seconds", "Duration of loading a snapshot from disk, in seconds.", ioDurationBuckets),
	}
}

func (pm *PrometheusPersistenceMetrics) SetPersistenceEnabled(enabled bool) {
	pm.persistenceEnabled.Set(boolToFloat64(enabled))
}

// Recovery

func (pm *PrometheusPersistenceMetrics) IncRecoveryRun() {
	pm.recoveryRuns.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncRecoveryFailure() {
	pm.recoveryFailures.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncRecoveredCommands(commands uint64) {
	pm.recoveredCommands.Add(float64(commands))
}

func (pm *PrometheusPersistenceMetrics) IncRecoveryRebaseline() {
	pm.recoveryRebaselines.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncRecoveryRebaselineFailure() {
	pm.recoveryRebaselineFailures.Inc()
}

// AOF

func (pm *PrometheusPersistenceMetrics) IncAofCommandWritten() {
	pm.aofCommandWritten.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncAofCommandWriteFailure() {
	pm.aofCommandWriteFailures.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncAofFsync() {
	pm.aofFsyncs.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncAofFsyncFailures() {
	pm.aofFsyncFailures.Inc()
}

// Journal

func (pm *PrometheusPersistenceMetrics) IncJournalRotation() {
	pm.journalRotations.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncJournalRotationFailure() {
	pm.journalRotationFailures.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncJournalReplay() {
	pm.journalReplays.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncJournalReplayFailure() {
	pm.journalReplayFailures.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncJournalReplaySkippedCommands() {
	pm.journalReplaySkippedCommands.Inc()
}

func (pm *PrometheusPersistenceMetrics) SetCurrentJournalSizeBytes(bytes uint64) {
	pm.currentJournalSize.Set(float64(bytes))
}

func (pm *PrometheusPersistenceMetrics) SetStandbyJournalSizeBytes(bytes uint64) {
	pm.standbyJournalSize.Set(float64(bytes))
}

func (pm *PrometheusPersistenceMetrics) SetCurrentGeneration(generation uint64) {
	pm.currentGeneration.Set(float64(generation))
}

func (pm *PrometheusPersistenceMetrics) SetCurrentSequenceID(sequenceID uint64) {
	pm.currentSequenceID.Set(float64(sequenceID))
}

// Checkpoint/Snapshot

func (pm *PrometheusPersistenceMetrics) IncCheckpointRun() {
	pm.checkpointRuns.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncCheckpointFailure() {
	pm.checkpointFailures.Inc()
}

func (pm *PrometheusPersistenceMetrics) IncCheckpointSuccess() {
	pm.checkpointSuccesses.Inc()
}

func (pm *PrometheusPersistenceMetrics) SetCheckpointInProgress(val bool) {
	pm.checkpointInProgress.Set(boolToFloat64(val))
}

func (pm *PrometheusPersistenceMetrics) SetLastCheckpointSucceeded(val bool) {
	pm.checkpointSucceeded.Set(boolToFloat64(val))
}

func (pm *PrometheusPersistenceMetrics) SetSnapshotBytesWritten(bytes uint64) {
	pm.snapshotBytesWritten.Set(float64(bytes))
}

// Latency Observers

func (pm *PrometheusPersistenceMetrics) ObserveAOFFsyncDuration(duration time.Duration) {
	pm.aofFsyncDuration.Observe(duration.Seconds())
}

func (pm *PrometheusPersistenceMetrics) ObserveRecoveryDuration(duration time.Duration) {
	pm.recoveryDuration.Observe(duration.Seconds())
}

func (pm *PrometheusPersistenceMetrics) ObserveRebaselineDuration(duration time.Duration) {
	pm.rebaselineDuration.Observe(duration.Seconds())
}

func (pm *PrometheusPersistenceMetrics) ObserveSnapshotSaveDuration(duration time.Duration) {
	pm.snapshotSaveDuration.Observe(duration.Seconds())
}

func (pm *PrometheusPersistenceMetrics) ObserveSnapshotLoadDuration(duration time.Duration) {
	pm.snapshotLoadDuration.Observe(duration.Seconds())
}

func (pm *PrometheusPersistenceMetrics) ObserveJournalReplayDuration(duration time.Duration) {
	pm.journalReplayDuration.Observe(duration.Seconds())
}

// collectors returns every metric owned by PrometheusPersistenceMetrics, for
// registration against a prometheus.Registry.
func (pm *PrometheusPersistenceMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		pm.persistenceEnabled,

		pm.recoveryRuns,
		pm.recoveryFailures,
		pm.recoveredCommands,
		pm.recoveryRebaselines,
		pm.recoveryRebaselineFailures,
		pm.recoveryDuration,

		pm.aofCommandWritten,
		pm.aofCommandWriteFailures,
		pm.aofFsyncs,
		pm.aofFsyncFailures,
		pm.aofFsyncDuration,

		pm.journalRotations,
		pm.journalRotationFailures,
		pm.journalReplays,
		pm.journalReplayFailures,
		pm.journalReplaySkippedCommands,
		pm.currentJournalSize,
		pm.standbyJournalSize,
		pm.currentGeneration,
		pm.currentSequenceID,
		pm.journalReplayDuration,

		pm.checkpointRuns,
		pm.checkpointFailures,
		pm.checkpointSuccesses,
		pm.checkpointInProgress,
		pm.checkpointSucceeded,
		pm.snapshotBytesWritten,
		pm.rebaselineDuration,
		pm.snapshotSaveDuration,
		pm.snapshotLoadDuration,
	}
}
