package store

import (
	"sync"
	"time"

	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
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

type ttlItem struct {
	key       string
	expiresAt time.Time
}

type Store struct {
	data    map[string]*entry        // Real data of key value
	cmdChan chan Command             // Command channel which Event Loop interacts with
	ttls    *heap.Heap[ttlItem]      // TTL heap
	pubsub  map[string][]chan []byte // Pubsub for different Clients
	mut     sync.Mutex               // Mutex for pubsub
}

func New() *Store {
	store := &Store{
		data:    make(map[string]*entry),
		cmdChan: make(chan Command),
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt.Before(b.expiresAt)
		}),
		pubsub: make(map[string][]chan []byte),
	}

	go store.eventLoop()
	go store.ttlEviction()

	return store
}

func (store *Store) CmdChan() chan<- Command {
	return store.cmdChan
}

func (store *Store) eventLoop() {
	for cmd := range store.cmdChan {
		var resp Response
		switch cmd.Name {

		}

		select {
		case cmd.Resp <- resp:
		default:
		}
	}
}

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
