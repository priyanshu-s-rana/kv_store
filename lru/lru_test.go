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

// ---- PeekBack ----

func TestPeekBackEmpty(t *testing.T) {
	lru := New()
	_, ok := lru.PeekBack()
	if ok {
		t.Errorf("expected false on empty LRU")
	}
}

func TestPeekBackSingleElement(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")

	key, ok := lru.PeekBack()
	if !ok || key != "a" {
		t.Errorf("PeekBack() = (%q, %v), want (\"a\", true)", key, ok)
	}
}

func TestPeekBackMultipleElements(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c") // order: c -> b -> a

	key, ok := lru.PeekBack()
	if !ok || key != "a" {
		t.Errorf("PeekBack() = (%q, %v), want (\"a\", true)", key, ok)
	}
	// Node must still be in the list and index after peek.
	assertOrder(t, lru, []string{"c", "b", "a"})
	if _, exists := lru.lruIndex["a"]; !exists {
		t.Errorf("'a' should still be in index after PeekBack")
	}
}

func TestPeekBackIsNonDestructive(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")

	key1, _ := lru.PeekBack()
	key2, _ := lru.PeekBack()
	if key1 != key2 {
		t.Errorf("PeekBack changed the list: first=%q second=%q", key1, key2)
	}
}

// ---- GetNode ----

func TestGetNodeFound(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")

	node := lru.GetNode("a")
	if node == nil {
		t.Fatalf("GetNode returned nil for existing key")
	}
	if node.GetData() != "a" {
		t.Errorf("node.GetData() = %q, want \"a\"", node.GetData())
	}
}

func TestGetNodeNotFound(t *testing.T) {
	lru := New()
	if node := lru.GetNode("missing"); node != nil {
		t.Errorf("GetNode returned non-nil for missing key")
	}
}

func TestGetNodeRemovedAfterRemove(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.Remove("a")

	if node := lru.GetNode("a"); node != nil {
		t.Errorf("GetNode returned non-nil after Remove")
	}
}

// ---- LRU eviction pattern ----

func TestLRUEvictionPattern(t *testing.T) {
	lru := New()
	lru.MoveToFront("a")
	lru.MoveToFront("b")
	lru.MoveToFront("c") // order: c -> b -> a

	// Access "a" — moves to front.
	lru.MoveToFront("a") // order: a -> c -> b

	// Peek and remove LRU (back = "b") — mirrors makeRoom behaviour.
	key, ok := lru.PeekBack()
	if !ok || key != "b" {
		t.Errorf("PeekBack() = (%q, %v), want (\"b\", true)", key, ok)
	}
	lru.Remove(key)
	assertOrder(t, lru, []string{"a", "c"})
	if lru.GetNode("b") != nil {
		t.Errorf("'b' should be gone from index after Remove")
	}
}
