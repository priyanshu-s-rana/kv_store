package store

import (
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
	"github.com/priyanshu-s-rana/kv_store/lru"
	"github.com/priyanshu-s-rana/kv_store/models"
	"github.com/priyanshu-s-rana/kv_store/utils"
)

type (
	Command        = models.Command
	Response       = models.Response
	SubscribeReq   = models.SubscribeReq
	UnsubscribeReq = models.UnsubscribeReq
)

type entry struct {
	value  []byte
	expiry int64
}

// Check if the entry is expired based on the current time and the expiry time
func (e *entry) isExpired() bool {
	if e.expiry == 0 {
		return false
	}
	return utils.AbsoluteTimeNow() >= e.expiry
}

// Check if the entry has an expiry time set
func (e *entry) hasExpiry() bool {
	return !(e.expiry == 0)
}

type ttlItem struct {
	key       string
	expiresAt int64
}

type Persistence interface {
	Append(name constants.CmdName, args []string) error
	Checkpoint(map[string]SnapshotEntry) error
	Rebaseline(map[string]SnapshotEntry) error
}

type Store struct {
	data            map[string]*entry        // Real data of key value
	cmdChan         <-chan Command           // Command channel which Event Loop interacts with
	subscribeChan   <-chan SubscribeReq      // Subscribe channel to recieve subscribe requests
	unsubscribeChan <-chan UnsubscribeReq    // Unsubscribe channel to recieve unsubscribe reqestus
	ttls            *heap.Heap[ttlItem]      // TTL heap
	pubsub          map[string][]chan []byte // Pubsub for different Clients
	snapResp        chan SnapshotResponse    // Channel for snapshot responses
	lru             *lru.LRU                 // LRU key eviction when memory is full
	memoryProfile   *MemoryProfile           // Memory Profiling to keep track of size
	persistence     Persistence              // Persistence for the store
	metrics         StoreMetrics             // Metrics for Prometheus
}

// New creates and returns a Store with its event loop and TTL eviction goroutines running.
func New(memorySize int64, cmdChan chan Command, subscribeChan chan SubscribeReq, unsubscribeChan chan UnsubscribeReq, persistence Persistence, metrics StoreMetrics) *Store {
	store := &Store{
		data:            make(map[string]*entry),
		cmdChan:         cmdChan,
		subscribeChan:   subscribeChan,
		unsubscribeChan: unsubscribeChan,
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt < b.expiresAt
		}),
		pubsub:        make(map[string][]chan []byte),
		snapResp:      make(chan SnapshotResponse, 1),
		lru:           lru.New(),
		memoryProfile: NewMemProfile(memorySize, metrics),
		persistence:   persistence,
		metrics:       metrics,
	}

	return store
}

func (store *Store) Start() {
	go store.eventLoop()
}

// eventLoop processes commands from cmdChan sequentially, ensuring single-threaded data access.
func (store *Store) eventLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			store.evict(nil)
		case cmd := <-store.cmdChan:
			store.handleCommand(&cmd)
		case req := <-store.subscribeChan:
			store.handleSubscribeReq(&req)
		case req := <-store.unsubscribeChan:
			store.handleUnsubscribeReq(&req)
		}
	}
}

func (store *Store) handleCommand(cmd *Command) {
	start := time.Now()
	store.metrics.IncCommandsExecuted(cmd.Name)

	normaliseCommand(cmd)
	var resp Response
	cmdMeta, ok := registry[cmd.Name]

	if !ok {
		resp = store._default(*cmd)
	} else {
		resp = cmdMeta.handler(store, cmd.Args)
		if cmdMeta.isWrite && !cmd.SkipAof && resp.IsError() == nil {
			store.persistence.Append(cmd.Name, cmd.Args)
		}
	}

	store.metrics.ObserveCommandDuration(cmd.Name, time.Since(start))
	if err := resp.IsError(); err != nil {
		store.metrics.IncCommandFailures(cmd.Name)
	}

	select {
	case cmd.Resp <- resp:
	default:
	}
}

func (store *Store) handleSubscribeReq(req *SubscribeReq) {
	start := time.Now()
	store.metrics.IncCommandsExecuted(constants.Subscribe)
	store.subscribe(req.Subscribers)

	store.metrics.ObserveCommandDuration(constants.Subscribe, time.Since(start))

	close(req.Done)
}

func (store *Store) handleUnsubscribeReq(req *UnsubscribeReq) {
	start := time.Now()
	store.metrics.IncCommandsExecuted(constants.Unsubscribe)
	store.unsubscribe(req.SubscribedTopics)
	close(req.Done)
	store.metrics.ObserveCommandDuration(constants.Unsubscribe, time.Since(start))
}
