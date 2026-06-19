package lru

import "testing"

// order returns the list from head to tail as a slice for easy assertions.
func order(lru *LRU) []string {
	var result []string
	node := lru.head
	for node != nil {
		result = append(result, node.GetData())
		node = node.GetNext()
	}
	return result
}

func assertOrder(t *testing.T, lru *LRU, want []string) {
	t.Helper()
	got := order(lru)
	if len(got) != len(want) {
		t.Errorf("list = %v, want %v", got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("list[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// ---- New ----

func TestNewIsEmpty(t *testing.T) {
	lru := New()
	if lru.head != nil {
		t.Errorf("head should be nil")
	}
	if lru.tail != nil {
		t.Errorf("tail should be nil")
	}
	if len(lru.lruIndex) != 0 {
		t.Errorf("index should be empty")
	}
}

// ---- MoveToFront ----

func TestMoveToFrontNewKey(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")

	if lru.head == nil || lru.head.GetData() != "a" {
		t.Errorf("head should be 'a'")
	}
	if lru.tail == nil || lru.tail.GetData() != "a" {
		t.Errorf("tail should be 'a'")
	}
	if _, ok := lru.lruIndex["a"]; !ok {
		t.Errorf("'a' should be in index")
	}
}

func TestMoveToFrontMultipleNewKeys(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c")

	assertOrder(t, lru, []string{"c", "b", "a"})
	if lru.tail.GetData() != "a" {
		t.Errorf("tail = %q, want 'a'", lru.tail.GetData())
	}
}

func TestMoveToFrontAlreadyHead(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("b") // already head — should be no-op

	assertOrder(t, lru, []string{"b", "a"})
}

func TestMoveToFrontExistingTail(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c")
	lru.MoveToFront("a") // was tail, moves to front

	assertOrder(t, lru, []string{"a", "c", "b"})
	if lru.tail.GetData() != "b" {
		t.Errorf("tail = %q, want 'b'", lru.tail.GetData())
	}
}

func TestMoveToFrontExistingMiddle(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c")
	lru.MoveToFront("b") // middle node moves to front

	assertOrder(t, lru, []string{"b", "c", "a"})
}

// ---- RemoveFromBack ----

func TestRemoveFromBackEmpty(t *testing.T) {
	lru := New()
	key, ok := lru.RemoveFromBack()
	if ok {
		t.Errorf("expected false on empty LRU, got key=%q", key)
	}
}

func TestRemoveFromBackSingleElement(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")

	key, ok := lru.RemoveFromBack()
	if !ok || key != "a" {
		t.Errorf("RemoveFromBack() = (%q, %v), want (\"a\", true)", key, ok)
	}
	if lru.head != nil || lru.tail != nil {
		t.Errorf("head and tail should be nil after removing last element")
	}
	if _, exists := lru.lruIndex["a"]; exists {
		t.Errorf("'a' should be removed from index")
	}
}

func TestRemoveFromBackMultipleElements(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c")

	key, ok := lru.RemoveFromBack()
	if !ok || key != "a" {
		t.Errorf("RemoveFromBack() = (%q, %v), want (\"a\", true)", key, ok)
	}
	assertOrder(t, lru, []string{"c", "b"})
	if lru.tail.GetData() != "b" {
		t.Errorf("tail = %q, want 'b'", lru.tail.GetData())
	}
	if _, exists := lru.lruIndex["a"]; exists {
		t.Errorf("'a' should be removed from index")
	}
}

func TestRemoveFromBackClearsIndex(t *testing.T) {
	lru := New()
	lru.MoveToFront("x")
	lru.MoveToFront("y")

	lru.RemoveFromBack() // removes "x"

	if _, exists := lru.lruIndex["x"]; exists {
		t.Errorf("evicted key 'x' still in index")
	}
	if _, exists := lru.lruIndex["y"]; !exists {
		t.Errorf("surviving key 'y' missing from index")
	}
}

// ---- LRU eviction pattern ----

func TestLRUEvictionPattern(t *testing.T) {
	lru := New()
	// Fill with 3 items
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c") // order: c -> b -> a

	// Access "a" — it should move to front
	lru.MoveToFront("a") // order: a -> c -> b

	// Evict LRU (back = "b")
	key, _ := lru.RemoveFromBack()
	if key != "b" {
		t.Errorf("evicted %q, want 'b'", key)
	}
	assertOrder(t, lru, []string{"a", "c"})
}
