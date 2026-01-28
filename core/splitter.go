package core

import (
	"github.com/dogeorg/dogewalker/spec"
	"github.com/dogeorg/governor"
)

type Splitter struct {
	governor.ServiceCtx
	chainEvents <-chan spec.BlockchainEvent
	channels    []chan spec.BlockchainEvent
}

// NewSplitter creates a channel fan-out splitter.
// Each channel will receive a copy of the event.
//
// All channels are treated as blocking channels,
// so the system will proceed at the rate of the slowest channel receiver.
// (DogeWalker is a non-blocking receiver, so it will not be the bottleneck.)
//
// `chainEvents` is the channel to receive events from.
// `chanSizes` is the size of each channel, e.g. []int{10, 100, 1000}.
//
// Returns the splitter service and the channels.
func NewSplitter(chainEvents <-chan spec.BlockchainEvent, chanSizes []int) (governor.Service, []chan spec.BlockchainEvent) {
	channels := make([]chan spec.BlockchainEvent, len(chanSizes))
	for n, chanSize := range chanSizes {
		channels[n] = make(chan spec.BlockchainEvent, chanSize)
	}
	s := Splitter{
		chainEvents: chainEvents,
		channels:    channels,
	}
	return &s, channels
}

func (s *Splitter) Run() {
	done := s.Context.Done()
	for {
		select {
		case <-done:
			return
		case event := <-s.chainEvents:
			for _, channel := range s.channels {
				channel <- event
			}
		}
	}
}
