package chaser

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/dogeorg/dogewalker/pkg/spec"
)

const (
	expectedBlockInterval = 90 * time.Second
)

/*
 * TipChaser tracks the current Best Block (Tip) of the blockchain.
 * It notifies all listeners when a new block is found.
 * It receives newTip messages from CoreZMQListener when there is a new Tip.
 * If it doesn't receive ZMQ notifications for a while, it will poll the node instead.
 *
 * A blocking listener will stall the TipChaser
 *
 * newTip: from CoreZMQListener (announces new Tip block hashes)
 */
type TipChaser struct {
	//util.ListenSet[string]
	listeners []Listener
	lock      sync.Mutex
}

type Listener struct {
	Channel chan string
	NoBlock bool // do not block (discard if channel full)
}

func (L *TipChaser) Listen(capacity int, noBlock bool) chan string {
	channel := make(chan string, capacity)
	L.lock.Lock()
	defer L.lock.Unlock()
	L.listeners = append(L.listeners, Listener{Channel: channel, NoBlock: noBlock})
	return channel
}

func (L *TipChaser) Announce(value string) {
	L.lock.Lock()
	defer L.lock.Unlock()
	for _, listener := range L.listeners {
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

func NewTipChaser(ctx context.Context, newTip <-chan string, client spec.Blockchain) *TipChaser {
	chaser := &TipChaser{}
	go func() {
		stop := ctx.Done()
		delay := time.NewTimer(expectedBlockInterval)
		lastid := ""
		for {
			select {
			case <-stop:
				if !delay.Stop() { // cancel timer
					<-delay.C // drain channel
				}
				return
			case blockid := <-newTip:
				if !delay.Stop() { // cancel timer
					<-delay.C // drain channel
				}
				if blockid != lastid {
					lastid = blockid
					chaser.Announce(blockid)
				}
				delay.Reset(expectedBlockInterval) // reschedule timer
			case <-delay.C:
				log.Println("TipChaser: falling back to getbestblockhash")
				blockid, err := client.GetBestBlockHash()
				if err != nil {
					log.Println("TipChaser: core RPC request failed: getbestblockhash")
				} else {
					if blockid != lastid {
						lastid = blockid
						chaser.Announce(blockid)
					}
				}
				delay.Reset(expectedBlockInterval) // reschedule timer
			}
		}
	}()
	return chaser
}
