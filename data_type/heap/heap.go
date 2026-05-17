package heap

type Heap[T any] struct {
	data []T
	comp func(a, b T) bool
}

func New[T any](comp func(a, b T) bool) *Heap[T] {
	return &Heap[T]{comp: comp}
}

// Length of the heap
func (h *Heap[T]) Len() int {
	return len(h.data)
}

// Pushing an element in the heap
// O(log n) complexity
func (h *Heap[T]) Push(ele T) {
	h.data = append(h.data, ele)

	h.shiftUp()
}

// Poping an element from the heap
// O(log n) complexity
func (h *Heap[T]) Pop() (T, bool) {
	var init T
	n := len(h.data)
	if n == 0 {
		return init, false
	}

	ele := h.data[0]
	h.data[0], h.data[n-1] = h.data[n-1], init
	h.data = h.data[:n-1]

	h.shiftDown()

	return ele, true
}

// Function for rearanging the top element according to the heap
func (h *Heap[T]) shiftDown() {
	heapLength := h.Len()
	if heapLength == 0 {
		return
	}

	i := 0
	for i < heapLength {
		idx := i
		left := 2*i + 1
		right := 2*i + 2
		if left < heapLength && h.comp(h.data[left], h.data[idx]) {
			idx = left
		}
		if right < heapLength && h.comp(h.data[right], h.data[idx]) {
			idx = right
		}

		if idx == i {
			return
		}

		// Swap
		h.data[i], h.data[idx] = h.data[idx], h.data[i]
		i = idx // move to the child
	}
}

// Function for rearanging the last element according to the heap
func (h *Heap[T]) shiftUp() {
	heapLength := h.Len()
	if heapLength == 0 {
		return
	}

	i := heapLength - 1
	for i > 0 {
		parent := (i - 1) / 2
		if !h.comp(h.data[i], h.data[parent]) {
			return
		}

		h.data[i], h.data[parent] = h.data[parent], h.data[i]
		i = parent
	}
}
