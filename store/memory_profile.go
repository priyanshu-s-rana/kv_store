package store

import (
	"fmt"
	"strings"

	"github.com/priyanshu-s-rana/kv_store/constants"
	linkedList "github.com/priyanshu-s-rana/kv_store/data_type/linked_list"
)

const (
	FIXED_OVERHEAD int64 = constants.LRU_OVERHEAD + constants.STORE_OVERHEAD + constants.SERVER_OVERHEAD + constants.HEAP_OVERHEAD
)

type MemoryProfile struct {
	maxBytes  int64 // Set when initialized
	peakBytes int64 // Peak Memory Size at any given time

	keyCount    int64 // Total key count in the memory (s.data)
	keyBytes    int64 // Memory acquired by key
	valueBytes  int64 // Memory acquired by value (also consist of entry overhead)
	ttlBytes    int64 // Memory acquired by ttl (ttl heap, also contains ttlItem overhead)
	lruBytes    int64 // Memory acquired by lru (lru index, also contain lru node overhead)
	pubsubBytes int64 // Memory acquired by pubsub
}

// NewMemProfile returns a MemoryProfile with the given byte limit.
// A limit of 0 means unlimited.
func NewMemProfile(memorySize int64) *MemoryProfile {
	return &MemoryProfile{maxBytes: memorySize}
}

func (memProf *MemoryProfile) updatePeakMemorySize() {
	currentMemorySize := memProf.currentMemorySize()
	if currentMemorySize > memProf.peakBytes {
		memProf.peakBytes = currentMemorySize
	}
}

// currentMemorySize returns the total tracked bytes including fixed struct overhead.
func (memProf *MemoryProfile) currentMemorySize() int64 {
	return FIXED_OVERHEAD + memProf.keyBytes + memProf.valueBytes + memProf.ttlBytes + memProf.lruBytes + memProf.pubsubBytes
}

// isOverLimit reports whether the current memory usage exceeds maxBytes.
// @returns false when maxBytes is 0 (unlimited).
func (memProf *MemoryProfile) isOverLimit() bool {
	if memProf.maxBytes <= 0 {
		return false
	}

	return memProf.currentMemorySize() > memProf.maxBytes
}

// recordDataSize charges the key and value bytes for a newly inserted entry.
func (memProf *MemoryProfile) recordDataSize(key string, e *entry) {
	memProf.keyBytes += constants.STRING_OVERHEAD + int64(len(key))
	memProf.valueBytes += constants.ENTRY_OVERHEAD + int64(len(e.value))
	memProf.keyCount++
	memProf.updatePeakMemorySize()
}

// updateValueSize adjusts valueBytes by the difference when an existing key's value changes.
func (memProf *MemoryProfile) updateValueSize(oldValue, newValue []byte) {
	memProf.valueBytes += int64(len(newValue)) - int64(len(oldValue))
	memProf.updatePeakMemorySize()
}

// recordDataRemove releases the key and value bytes when an entry is deleted.
func (memProf *MemoryProfile) recordDataRemove(key string, e *entry) {
	if key == "" || e == nil {
		return
	}
	memProf.keyCount--
	memProf.keyBytes -= constants.STRING_OVERHEAD + int64(len(key))
	memProf.valueBytes -= constants.ENTRY_OVERHEAD + int64(len(e.value))

	_resetToZeroIfNegative(&memProf.keyCount)
	_resetToZeroIfNegative(&memProf.keyBytes)
	_resetToZeroIfNegative(&memProf.valueBytes)
}

// recordTTLSize charges the TTL heap bytes for a new expiry entry. nil is a no-op.
func (memProf *MemoryProfile) recordTTLSize(item *ttlItem) {
	if item == nil {
		return
	}
	memProf.ttlBytes += constants.TTL_ITEM_OVERHEAD + int64(len(item.key))
	memProf.updatePeakMemorySize()
}

// recordTTLRemove releases the TTL heap bytes when an expiry entry is popped. nil is a no-op.
func (memProf *MemoryProfile) recordTTLRemove(item *ttlItem) {
	if item == nil {
		return
	}
	memProf.ttlBytes -= constants.TTL_ITEM_OVERHEAD + int64(len(item.key))
	_resetToZeroIfNegative(&memProf.ttlBytes)

}

// recordLRUSize charges the LRU index bytes for a newly tracked node. nil is a no-op.
func (memProf *MemoryProfile) recordLRUSize(node *linkedList.List[string]) {
	if node == nil {
		return
	}
	memProf.lruBytes += constants.LRU_NODE_OVERHEAD + int64(len(node.GetData()))
	memProf.updatePeakMemorySize()
}

// recordLRURemove releases the LRU index bytes when a node is evicted or deleted. nil is a no-op.
func (memProf *MemoryProfile) recordLRURemove(node *linkedList.List[string]) {
	if node == nil {
		return
	}
	memProf.lruBytes -= constants.LRU_NODE_OVERHEAD + int64(len(node.GetData()))
	_resetToZeroIfNegative(&memProf.lruBytes)
}

// recordPubSubTopicSize charges pubsubBytes for a newly created topic. Empty topic is a no-op.
func (memProf *MemoryProfile) recordPubSubTopicSize(topic string) {
	if topic == "" {
		return
	}
	memProf.pubsubBytes += constants.STRING_OVERHEAD + int64(len(topic))
	memProf.updatePeakMemorySize()
}

// recordPubSubTopicRemove releases pubsubBytes for a topic that has no remaining subscribers. Empty topic is a no-op.
func (memProf *MemoryProfile) recordPubSubTopicRemove(topic string) {
	if topic == "" {
		return
	}
	memProf.pubsubBytes -= constants.STRING_OVERHEAD + int64(len(topic))
	_resetToZeroIfNegative(&memProf.pubsubBytes)
}

// recordPubSubSubscriber charges pubsubBytes for one new subscriber channel.
func (memProf *MemoryProfile) recordPubSubSubscriber() {
	memProf.pubsubBytes += constants.BYTE_CHANNEL_OVERHEAD // chan []byte
	memProf.updatePeakMemorySize()
}

// recordPubSubSubscriberRemove releases pubsubBytes for one departing subscriber channel.
func (memProf *MemoryProfile) recordPubSubSubscriberRemove() {
	memProf.pubsubBytes -= constants.BYTE_CHANNEL_OVERHEAD
	_resetToZeroIfNegative(&memProf.pubsubBytes)
}

// _resetToZeroIfNegative clamps memorySize to 0 if it went negative due to accounting drift.
func _resetToZeroIfNegative(memorySize *int64) {
	if *memorySize < 0 {
		*memorySize = 0
	}
}

// resetAll zeroes all dynamic memory fields. Used by FLUSHALL to reset accounting after clearing the store.
func (memProf *MemoryProfile) resetAll() {
	memProf.keyCount = 0
	memProf.keyBytes = 0
	memProf.valueBytes = 0
	memProf.ttlBytes = 0
	memProf.lruBytes = 0
	memProf.pubsubBytes = 0
}

// getStats returns all memory fields as a newline-separated string of "label: value" pairs.
func (memProf *MemoryProfile) getStats() string {
	stats := []string{
		fmt.Sprintf("currentSize: %d B", memProf.currentMemorySize()),
		fmt.Sprintf("peakSize: %d B", memProf.peakBytes),
		fmt.Sprintf("maxSize: %d B", memProf.maxBytes),
		fmt.Sprintf("keyCount: %d", memProf.keyCount),
		fmt.Sprintf("keySize: %d B", memProf.keyBytes),
		fmt.Sprintf("valueSize: %d B", memProf.valueBytes),
		fmt.Sprintf("ttlSize: %d B", memProf.ttlBytes),
		fmt.Sprintf("lruSize: %d B", memProf.lruBytes),
		fmt.Sprintf("pubsubSize: %d B", memProf.pubsubBytes),
	}
	return strings.Join(stats, "\n")
}
