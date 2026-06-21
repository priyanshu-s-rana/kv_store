package store

import (
	"sync"
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

type Store struct {
	data          map[string]*entry        // Real data of key value
	cmdChan       chan Command             // Command channel which Event Loop interacts with
	ttls          *heap.Heap[ttlItem]      // TTL heap
	pubsub        map[string][]chan []byte // Pubsub for different Clients
	mut           sync.Mutex               // Mutex for pubsub
	snapResp      chan SnapshotResponse    // Channel for snapshot responses
	lru           *lru.LRU                 // LRU key eviction when memory is full
	memoryProfile *MemoryProfile           // Memory Profiling to keep track of size
}

// New creates and returns a Store with its event loop and TTL eviction goroutines running.
func New(memorySize int64) *Store {
	store := &Store{
		data:    make(map[string]*entry),
		cmdChan: make(chan Command),
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt.Before(b.expiresAt)
		}),
		pubsub:        make(map[string][]chan []byte),
		snapResp:      make(chan SnapshotResponse, 1),
		lru:           lru.New(),
		memoryProfile: NewMemProfile(memorySize),
	}

	go store.eventLoop()
	go store.ttlEviction()

	return store
}

// CmdChan returns a write-only channel for submitting commands to the event loop.
func (store *Store) CmdChan() chan<- Command {
	return store.cmdChan
}

// eventLoop processes commands from cmdChan sequentially, ensuring single-threaded data access.
func (store *Store) eventLoop() {
	for cmd := range store.cmdChan {
		var resp Response
		switch cmd.Name {
		case constants.Ping:
			resp = store.ping()
		case constants.Set:
			resp = store.set(cmd.Args)
		case constants.Get:
			resp = store.get(cmd.Args)
		case constants.Del:
			resp = store.del(cmd.Args)
		case constants.TTL:
			resp = store.ttl(cmd.Args)
		case constants.Expire:
			resp = store.expire(cmd.Args)
		case constants.Publish:
			resp = store.publish(cmd.Args)
		case constants.Keys:
			resp = store.keys(cmd.Args)
		case constants.FlushAll:
			resp = store.flushAll()
		case constants.MemoryStats:
			resp = store.memoryStats()
		case constants.Mget:
			resp = store.mget(cmd.Args)
		case constants.Mset:
			resp = store.mset(cmd.Args)
		case constants.Incr:
			resp = store.incr(cmd.Args)
		case constants.Decr:
			resp = store.decr(cmd.Args)
		case constants.EVICT:
			store.evict()
			continue
		case constants.Snapshot:
			store.snapshot()
			continue
		default:
			resp = store._default(cmd)
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
			Name: "_EVICT",
			Resp: make(chan Response, 1),
		}
	}
}
