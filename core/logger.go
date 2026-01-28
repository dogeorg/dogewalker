package core

import (
	"fmt"

	"github.com/dogeorg/dogewalker/spec"
	"github.com/dogeorg/governor"
)

type Logger struct {
	governor.ServiceCtx
	chainEvents <-chan spec.BlockchainEvent
}

func NewLogger(chainEvents <-chan spec.BlockchainEvent) governor.Service {
	return &Logger{chainEvents: chainEvents}
}

func (l *Logger) Run() {
	done := l.Context.Done()
	for {
		select {
		case <-done:
			return
		case event := <-l.chainEvents:
			switch event.Event {
			case spec.EventTypeBlock:
				fmt.Printf("[Logger] block: %x\n", event.Hash)
			case spec.EventTypeTx:
				fmt.Printf("[Logger] tx: %x\n", event.Hash)
			default:
				fmt.Printf("[Logger] unknown event: %d\n", event.Event)
			}
		}
	}
}
