package models

import (
	"sync"
	"time"

	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
)

type Command struct {
	Client string
	Args   []string
	Resp   chan Response
}

type Response struct {
	Value []string
	Err   error
}

type entry struct {
	value  []byte
	expiry time.Time
}

type ttlItem struct {
	key       string
	expiresAt time.Time
}

type Store struct {
	data    map[string]*entry
	cmdChan chan Command
	ttls    *heap.Heap[ttlItem]
	pubsub  map[string][]chan []byte
	mut     sync.Mutex
}

func New() *Store {
	return &Store{
		data:    make(map[string]*entry),
		cmdChan: make(chan Command),
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt.Before(b.expiresAt)
		}),
		pubsub: make(map[string][]chan []byte),
	}
}
