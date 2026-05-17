package heap

import (
	"math/rand"
	"sort"
	"testing"
)

func minIntHeap() *Heap[int] {
	return New[int](func(a, b int) bool { return a < b })
}

func maxIntHeap() *Heap[int] {
	return New[int](func(a, b int) bool { return a > b })
}

func TestMakeHeap(t *testing.T) {
	arr := []int{4, 9, 2, 10}
	h := minIntHeap()
	for _, v := range arr {
		h.Push(v)
	}

	if got, want := h.Len(), len(arr); got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
}

func TestPopEmpty(t *testing.T) {
	h := minIntHeap()
	v, ok := h.Pop()
	if ok {
		t.Errorf("Pop on empty returned ok=true, value=%v", v)
	}
	if v != 0 {
		t.Errorf("Pop on empty returned value=%d, want zero value 0", v)
	}
	if h.Len() != 0 {
		t.Errorf("Len after empty pop = %d, want 0", h.Len())
	}
}

func TestSingleElement(t *testing.T) {
	h := minIntHeap()
	h.Push(42)

	if h.Len() != 1 {
		t.Fatalf("Len after push = %d, want 1", h.Len())
	}

	v, ok := h.Pop()
	if !ok || v != 42 {
		t.Errorf("Pop = (%d, %v), want (42, true)", v, ok)
	}
	if h.Len() != 0 {
		t.Errorf("Len after pop = %d, want 0", h.Len())
	}
}

func TestMinHeapPopOrder(t *testing.T) {
	in := []int{5, 3, 8, 1, 9, 2, 7, 4, 6}
	h := minIntHeap()
	for _, v := range in {
		h.Push(v)
	}

	want := append([]int(nil), in...)
	sort.Ints(want)

	got := make([]int, 0, len(in))
	for h.Len() > 0 {
		v, _ := h.Pop()
		got = append(got, v)
	}

	if !equalInts(got, want) {
		t.Errorf("Pop order = %v, want ascending %v", got, want)
	}
}

func TestMaxHeapPopOrder(t *testing.T) {
	in := []int{5, 3, 8, 1, 9, 2, 7, 4, 6}
	h := maxIntHeap()
	for _, v := range in {
		h.Push(v)
	}

	want := append([]int(nil), in...)
	sort.Sort(sort.Reverse(sort.IntSlice(want)))

	got := make([]int, 0, len(in))
	for h.Len() > 0 {
		v, _ := h.Pop()
		got = append(got, v)
	}

	if !equalInts(got, want) {
		t.Errorf("Pop order = %v, want descending %v", got, want)
	}
}

func TestDuplicates(t *testing.T) {
	in := []int{3, 1, 3, 2, 1, 2, 3, 1}
	h := minIntHeap()
	for _, v := range in {
		h.Push(v)
	}

	want := []int{1, 1, 1, 2, 2, 3, 3, 3}
	got := make([]int, 0, len(in))
	for h.Len() > 0 {
		v, _ := h.Pop()
		got = append(got, v)
	}

	if !equalInts(got, want) {
		t.Errorf("Pop with dups = %v, want %v", got, want)
	}
}

func TestInterleavedPushPop(t *testing.T) {
	h := minIntHeap()
	h.Push(5)
	h.Push(3)
	if v, _ := h.Pop(); v != 3 {
		t.Errorf("Pop = %d, want 3", v)
	}
	h.Push(1)
	h.Push(4)
	if v, _ := h.Pop(); v != 1 {
		t.Errorf("Pop = %d, want 1", v)
	}
	if v, _ := h.Pop(); v != 4 {
		t.Errorf("Pop = %d, want 4", v)
	}
	if v, _ := h.Pop(); v != 5 {
		t.Errorf("Pop = %d, want 5", v)
	}
	if _, ok := h.Pop(); ok {
		t.Errorf("Pop on drained heap returned ok=true")
	}
}

func TestStructType(t *testing.T) {
	type Task struct {
		Name     string
		Priority int
	}

	h := New[Task](func(a, b Task) bool { return a.Priority < b.Priority })
	h.Push(Task{"low", 10})
	h.Push(Task{"urgent", 1})
	h.Push(Task{"medium", 5})

	v, ok := h.Pop()
	if !ok || v.Name != "urgent" {
		t.Errorf("Pop = %+v, want urgent task", v)
	}
	v, _ = h.Pop()
	if v.Name != "medium" {
		t.Errorf("Pop = %+v, want medium task", v)
	}
	v, _ = h.Pop()
	if v.Name != "low" {
		t.Errorf("Pop = %+v, want low task", v)
	}
}

func TestStringHeap(t *testing.T) {
	h := New[string](func(a, b string) bool { return a < b })
	for _, s := range []string{"banana", "apple", "cherry", "date"} {
		h.Push(s)
	}

	want := []string{"apple", "banana", "cherry", "date"}
	for _, w := range want {
		v, ok := h.Pop()
		if !ok || v != w {
			t.Errorf("Pop = (%q, %v), want (%q, true)", v, ok, w)
		}
	}
}

func TestStress(t *testing.T) {
	const n = 10_000
	r := rand.New(rand.NewSource(42))

	in := make([]int, n)
	for i := range in {
		in[i] = r.Intn(100_000)
	}

	h := minIntHeap()
	for _, v := range in {
		h.Push(v)
	}

	if h.Len() != n {
		t.Fatalf("Len = %d, want %d", h.Len(), n)
	}

	prev, _ := h.Pop()
	for h.Len() > 0 {
		v, _ := h.Pop()
		if v < prev {
			t.Fatalf("Heap property violated: %d came after %d", v, prev)
		}
		prev = v
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
