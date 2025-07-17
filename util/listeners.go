package util

import "sync"

type ListenSet[T any] struct {
	Listeners []Listener[T]
	Lock      sync.Mutex
}

type Listener[T any] struct {
	Channel chan T
	NoBlock bool // do not block (discard if channel full)
}

func (L *ListenSet[T]) Listen(capacity int, noBlock bool) chan T {
	channel := make(chan T, capacity)
	L.Lock.Lock()
	defer L.Lock.Unlock()
	L.Listeners = append(L.Listeners, Listener[T]{Channel: channel, NoBlock: noBlock})
	return channel
}

func (L *ListenSet[T]) AddListener(channel chan T, noBlock bool) {
	L.Lock.Lock()
	defer L.Lock.Unlock()
	L.Listeners = append(L.Listeners, Listener[T]{Channel: channel, NoBlock: noBlock})
}

func (L *ListenSet[T]) RemoveListener(channel chan T) {
	L.Lock.Lock()
	defer L.Lock.Unlock()
	// unordered remove.
	a := L.Listeners
	for i := 0; i < len(a); i++ {
		if a[i].Channel == channel {
			a[i] = a[len(a)-1]
			L.Listeners = a[:len(a)-1]
			break
		}
	}
}

func (L *ListenSet[T]) Announce(value T) {
	L.Lock.Lock()
	defer L.Lock.Unlock()
	for _, listener := range L.Listeners {
		if listener.NoBlock {
			select {
			case listener.Channel <- value:
			default:
			}
		} else {
			listener.Channel <- value
		}
	}
}
