package store

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
	"github.com/priyanshu-s-rana/kv_store/lru"
	"github.com/priyanshu-s-rana/kv_store/models"
)

type (
	Command  = models.Command
	Response = models.Response
)

type entry struct {
	value  []byte
	expiry time.Time
}

// Check if the entry is expired based on the current time and the expiry time
func (e *entry) isExpired() bool {
	if e.expiry.IsZero() {
		return false
	}
	return time.Now().After(e.expiry)
}

// Check if the entry has an expiry time set
func (e *entry) hasExpiry() bool {
	return !e.expiry.IsZero()
}

type ttlItem struct {
	key       string
	expiresAt time.Time
}

type Persistence interface {
	Append(name constants.CmdName, args []string) error
	Checkpoint(map[string]SnapshotEntry) error
	CheckpointSuccess() error
	RebaseLine(map[string]SnapshotEntry) error
}

type pubSubStats struct {
	activeTopics      atomic.Int64
	activeSubscribers atomic.Int64
	messagesPublished atomic.Int64
}

type Store struct {
	data          map[string]*entry        // Real data of key value
	cmdChan       chan Command             // Command channel which Event Loop interacts with
	ttls          *heap.Heap[ttlItem]      // TTL heap
	pubsub        map[string][]chan []byte // Pubsub for different Clients
	mut           sync.Mutex               // Mutex for pubsub
	snapResp      chan SnapshotResponse    // Channel for snapshot responses
	lru           *lru.LRU                 // LRU key eviction when memory is full
	memoryProfile *MemoryProfile           // Memory Profiling to keep track of size
	persistence   Persistence
	pubSubStats   *pubSubStats
}

// New creates and returns a Store with its event loop and TTL eviction goroutines running.
func New(memorySize int64, cmdChan chan Command, persistence Persistence) *Store {
	store := &Store{
		data:    make(map[string]*entry),
		cmdChan: cmdChan,
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt.Before(b.expiresAt)
		}),
		pubsub:        make(map[string][]chan []byte),
		snapResp:      make(chan SnapshotResponse, 1),
		lru:           lru.New(),
		memoryProfile: NewMemProfile(memorySize),
		pubSubStats:   &pubSubStats{},
		persistence:   persistence,
	}

	return store
}

func (store *Store) Start() {
	go store.eventLoop()
	go store.ttlEviction()
}

// eventLoop processes commands from cmdChan sequentially, ensuring single-threaded data access.
func (store *Store) eventLoop() {
	for cmd := range store.cmdChan {
		var resp Response
		cmdMeta, ok := Registry[cmd.Name]
		if !ok {
			resp = store._default(cmd)
		} else {
			resp = cmdMeta.Handler(store, cmd.Args)
			if cmdMeta.IsWrite && !cmd.SkipAof {
				store.persistence.Append(cmd.Name, cmd.Args)
			}
		}

		select {
		case cmd.Resp <- resp:
		default:
		}
	}
}

// ttlEviction ticks every second and sends an internal EVICT command to prune expired keys.
func (store *Store) ttlEviction() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		store.cmdChan <- Command{
			Name: constants.EVICT,
			Resp: make(chan Response, 1),
		}
	}
}
