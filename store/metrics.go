package store

import (
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// StoreMetrics is the domain-level metrics contract for the Store subsystem.
// Implementations translate these events/state changes into an observability
// backend (e.g. Prometheus); the Store package has no knowledge of that backend.
type StoreMetrics interface {
	// Commands
	// The Store executes commands handed to it by the event loop; it does not
	// receive them off the wire (that is the Server's responsibility).
	IncCommandsExecuted(constants.CmdName)
	IncCommandFailures(constants.CmdName)

	// Time spent executing a command inside the Store.
	// This excludes parsing, networking, persistence, etc.
	ObserveCommandDuration(constants.CmdName, time.Duration)

	// Memory
	SetCurrentMemoryBytes(int64)
	SetPeakMemoryBytes(int64)
	SetMaxMemoryBytes(int64)
	SetMemoryUtilization(float32)
	SetKeyCount(int64)
	SetKeyBytes(int64)
	SetValueBytes(int64)
	SetTTLBytes(int64)
	SetLRUBytes(int64)
	SetPubSubBytes(int64)

	// TTL
	IncExpiredKeys()
	ObserveTTLExpiryDuration(time.Duration)

	// Pub/Sub
	SetActiveTopics(int64)
	SetActiveSubscribers(int64)
	IncMessagesPublished()
	ObservePublishDuration(time.Duration)
}
