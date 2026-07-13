package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrometheusPersistenceMetricsFlags(t *testing.T) {
	m := NewPrometheusPersistenceMetrics()

	m.SetPersistenceEnabled(true)
	if got := testutil.ToFloat64(m.persistenceEnabled); got != 1 {
		t.Errorf("persistenceEnabled = %v, want 1", got)
	}
	m.SetPersistenceEnabled(false)
	if got := testutil.ToFloat64(m.persistenceEnabled); got != 0 {
		t.Errorf("persistenceEnabled = %v, want 0", got)
	}

	m.SetCheckpointInProgress(true)
	if got := testutil.ToFloat64(m.checkpointInProgress); got != 1 {
		t.Errorf("checkpointInProgress = %v, want 1", got)
	}

	m.SetLastCheckpointSucceeded(false)
	if got := testutil.ToFloat64(m.checkpointSucceeded); got != 0 {
		t.Errorf("checkpointSucceeded = %v, want 0", got)
	}
}

func TestPrometheusPersistenceMetricsRecoveryCounters(t *testing.T) {
	m := NewPrometheusPersistenceMetrics()

	m.IncRecoveryRun()
	m.IncRecoveryFailure()
	m.IncRecoveredCommands(5)
	m.IncRecoveredCommands(3)
	m.IncRecoveryRebaseline()
	m.IncRecoveryRebaselineFailure()

	cases := []struct {
		name string
		got  prometheus.Counter
		want float64
	}{
		{"recoveryRuns", m.recoveryRuns, 1},
		{"recoveryFailures", m.recoveryFailures, 1},
		{"recoveredCommands", m.recoveredCommands, 8},
		{"recoveryRebaselines", m.recoveryRebaselines, 1},
		{"recoveryRebaselineFailures", m.recoveryRebaselineFailures, 1},
	}
	for _, c := range cases {
		if got := testutil.ToFloat64(c.got); got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrometheusPersistenceMetricsAOFCounters(t *testing.T) {
	m := NewPrometheusPersistenceMetrics()

	m.IncAofCommandWritten()
	m.IncAofCommandWriteFailure()
	m.IncAofFsync()
	m.IncAofFsyncFailures()
	m.ObserveAOFFsyncDuration(time.Millisecond)

	if got := testutil.ToFloat64(m.aofCommandWritten); got != 1 {
		t.Errorf("aofCommandWritten = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.aofCommandWriteFailures); got != 1 {
		t.Errorf("aofCommandWriteFailures = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.aofFsyncs); got != 1 {
		t.Errorf("aofFsyncs = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.aofFsyncFailures); got != 1 {
		t.Errorf("aofFsyncFailures = %v, want 1", got)
	}
	if count := histogramSampleCount(t, m.aofFsyncDuration); count != 1 {
		t.Errorf("aofFsyncDuration sample count = %d, want 1", count)
	}
}

func TestPrometheusPersistenceMetricsJournalGaugesAndCounters(t *testing.T) {
	m := NewPrometheusPersistenceMetrics()

	m.IncJournalRotation()
	m.IncJournalRotationFailure()
	m.IncJournalReplay()
	m.IncJournalReplayFailure()
	m.IncJournalReplaySkippedCommands()
	m.SetCurrentJournalSizeBytes(1024)
	m.SetStandbyJournalSizeBytes(512)
	m.SetCurrentGeneration(3)
	m.SetCurrentSequenceID(99)
	m.ObserveJournalReplayDuration(time.Millisecond)

	if got := testutil.ToFloat64(m.journalRotations); got != 1 {
		t.Errorf("journalRotations = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.journalRotationFailures); got != 1 {
		t.Errorf("journalRotationFailures = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.journalReplays); got != 1 {
		t.Errorf("journalReplays = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.journalReplayFailures); got != 1 {
		t.Errorf("journalReplayFailures = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.journalReplaySkippedCommands); got != 1 {
		t.Errorf("journalReplaySkippedCommands = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.currentJournalSize); got != 1024 {
		t.Errorf("currentJournalSize = %v, want 1024", got)
	}
	if got := testutil.ToFloat64(m.standbyJournalSize); got != 512 {
		t.Errorf("standbyJournalSize = %v, want 512", got)
	}
	if got := testutil.ToFloat64(m.currentGeneration); got != 3 {
		t.Errorf("currentGeneration = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.currentSequenceID); got != 99 {
		t.Errorf("currentSequenceID = %v, want 99", got)
	}
	if count := histogramSampleCount(t, m.journalReplayDuration); count != 1 {
		t.Errorf("journalReplayDuration sample count = %d, want 1", count)
	}
}

func TestPrometheusPersistenceMetricsCheckpointAndSnapshot(t *testing.T) {
	m := NewPrometheusPersistenceMetrics()

	m.IncCheckpointRun()
	m.IncCheckpointFailure()
	m.IncCheckpointSuccess()
	m.SetSnapshotBytesWritten(2048)
	m.ObserveRecoveryDuration(time.Millisecond)
	m.ObserveRebaselineDuration(time.Millisecond)
	m.ObserveSnapshotSaveDuration(time.Millisecond)
	m.ObserveSnapshotLoadDuration(time.Millisecond)

	if got := testutil.ToFloat64(m.checkpointRuns); got != 1 {
		t.Errorf("checkpointRuns = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.checkpointFailures); got != 1 {
		t.Errorf("checkpointFailures = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.checkpointSuccesses); got != 1 {
		t.Errorf("checkpointSuccesses = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.snapshotBytesWritten); got != 2048 {
		t.Errorf("snapshotBytesWritten = %v, want 2048", got)
	}
	if count := histogramSampleCount(t, m.recoveryDuration); count != 1 {
		t.Errorf("recoveryDuration sample count = %d, want 1", count)
	}
	if count := histogramSampleCount(t, m.rebaselineDuration); count != 1 {
		t.Errorf("rebaselineDuration sample count = %d, want 1", count)
	}
	if count := histogramSampleCount(t, m.snapshotSaveDuration); count != 1 {
		t.Errorf("snapshotSaveDuration sample count = %d, want 1", count)
	}
	if count := histogramSampleCount(t, m.snapshotLoadDuration); count != 1 {
		t.Errorf("snapshotLoadDuration sample count = %d, want 1", count)
	}
}

func TestPrometheusPersistenceMetricsCollectorsRegisterCleanly(t *testing.T) {
	m := NewPrometheusPersistenceMetrics()
	registry := prometheus.NewRegistry()

	registry.MustRegister(m.collectors()...)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != len(m.collectors()) {
		t.Errorf("gathered %d metric families, want %d (one per collector; persistence has no Vec collectors)", len(families), len(m.collectors()))
	}
}
