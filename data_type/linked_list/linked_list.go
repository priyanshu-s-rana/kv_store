package linkedlist

// Doubly Linked List
type List[T any] struct {
	next, prev *List[T]
	data       T
}

func New[T any](data T) *List[T] {
	return &List[T]{
		next: nil,
		prev: nil,
		data: data,
	}
}

func (list *List[T]) Next(node *List[T]) {
	list.next = node
}

func (list *List[T]) Prev(node *List[T]) {
	list.prev = node
}

func (list *List[T]) GetNext() *List[T] { return list.next }

func (list *List[T]) GetPrev() *List[T] { return list.prev }

func (list *List[T]) GetData() T { return list.data }
