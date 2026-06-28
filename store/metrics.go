package store

import "time"

// ======== MEMORY STATS ========
type MemoryStats struct {
	CurrentBytes int64
	PeakBytes    int64
	MaxBytes     int64
	Utilization  float32

	KeyCount int64

	KeyBytes    int64
	ValueBytes  int64
	TTLBytes    int64
	LRUBytes    int64
	PubSubBytes int64
}

// GetStats returns a plain snapshot of the current memory profile.
func (memProf *MemoryProfile) GetStats() MemoryStats {
	current := memProf.currentMemorySize()
	var utilization float32
	if memProf.maxBytes > 0 {
		utilization = float32(current) / float32(memProf.maxBytes) * 100
	}
	return MemoryStats{
		CurrentBytes: current,
		PeakBytes:    memProf.peakBytes,
		MaxBytes:     memProf.maxBytes,
		Utilization:  utilization,
		KeyCount:     memProf.keyCount,
		KeyBytes:     memProf.keyBytes,
		ValueBytes:   memProf.valueBytes,
		TTLBytes:     memProf.ttlBytes,
		LRUBytes:     memProf.lruBytes,
		PubSubBytes:  memProf.pubsubBytes,
	}
}

// ======== SNAPSHOT STATS ========
type SnapshotStats struct {
	SnapshotCount        int64
	SnapshotFailures     int64
	SnapshotInProgress   bool
	LastSnapshotTime     time.Time
	LastSnapshotDuration time.Duration
	SnapshotSizeBytes    int64
}

func (snpStats *SnapshotStats) GetStats() SnapshotStats {
	return *snpStats
}

// ======== PUB/SUB STATS ========
type PubSubStats struct {
	ActiveTopics      int64
	ActiveSubscribers int64
	MessagesPublished int64
}

func (pubSubStats *pubSubStats) GetStats() PubSubStats {
	return PubSubStats{
		ActiveTopics:      pubSubStats.activeTopics.Load(),
		ActiveSubscribers: pubSubStats.activeSubscribers.Load(),
		MessagesPublished: pubSubStats.messagesPublished.Load(),
	}
}
