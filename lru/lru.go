package lru

import (
	linkedList "github.com/priyanshu-s-rana/kv_store/data_type/linked_list"
)

type LRU struct {
	head, tail *linkedList.List[string]
	lruIndex   map[string]*linkedList.List[string]
}

// New returns an empty LRU cache with an initialised index.
func New() *LRU {
	return &LRU{
		head:     nil,
		tail:     nil,
		lruIndex: make(map[string]*linkedList.List[string]),
	}
}

// MoveToFront marks key as most-recently used by moving it to the head of the list.
// Creates a new node if the key is not already tracked.
func (lru *LRU) MoveToFront(key string) {
	node, exists := lru.getOrCreate(key)
	if exists {
		if node == lru.head {
			return
		}
		lru.unlink(node)
	}

	lru.pushFront(node)
}

// RemoveFromBack evicts the least-recently-used key from the tail of the list.
// @returns key, true: if an entry was evicted,
// @returns "", false: if the cache is empty.
func (lru *LRU) RemoveFromBack() (string, bool) {
	if lru.tail != nil {
		node := lru.tail
		lru.unlink(node)

		delete(lru.lruIndex, node.GetData())
		return node.GetData(), true
	}

	return "", false
}

// Remove deletes a specific key from the cache without evicting by recency order.
// No-op if the key is not tracked.
func (lru *LRU) Remove(key string) {
	if node, ok := lru.lruIndex[key]; ok {
		lru.unlink(node)
		delete(lru.lruIndex, key)
	}
}

// getOrCreate returns the existing node for key, or allocates and indexes a new one.
// @returns node, true: if the key already existed,
// @returns node, false: if a new node was created.
func (lru *LRU) getOrCreate(key string) (*linkedList.List[string], bool) {
	if node, ok := lru.lruIndex[key]; ok {
		return node, true
	}

	node := linkedList.New[string](key)
	lru.lruIndex[key] = node
	return node, false
}

// pushFront inserts node at the head of the list.
func (lru *LRU) pushFront(node *linkedList.List[string]) {
	node.Next(lru.head)
	if lru.head != nil {
		lru.head.Prev(node)
	}

	lru.head = node

	if lru.tail == nil {
		lru.tail = node
	}
}

// unlink removes node from the doubly linked list, updating head/tail as needed.
func (lru *LRU) unlink(node *linkedList.List[string]) {
	if node.GetPrev() != nil {
		node.GetPrev().Next(node.GetNext())
	} else {
		lru.head = node.GetNext()
	}

	if node.GetNext() != nil {
		node.GetNext().Prev(node.GetPrev())
	} else {
		lru.tail = node.GetPrev()
	}

	node.Next(nil)
	node.Prev(nil)
}
