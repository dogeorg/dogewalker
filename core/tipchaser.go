package core

import (
	"bytes"
	"log"
	"syscall"
	"time"

	"github.com/dogeorg/dogewalker/spec"
	"github.com/dogeorg/governor"
	"github.com/pebbe/zmq4"
)

const RETRY_DELAY = 5 * time.Second // for connect errors.
const ERROR_DELAY = 1 * time.Second // for ZMQ errors.

/*
 * NewTipChaser listens to Core Node ZMQ interface.
 *
 * listener channel announces whenever Core finds a new Best Block Hash (Tip change)
 * set includeTx if you want to receive transaction events as well
 * set doNotBlock if you don't want to block when the listener channel is full
 *
 * `coreAddrZMQ` is the TCP address of Core ZMQ, e.g. "tcp://127.0.0.1:28332"
 */
func NewTipChaser(coreAddrZMQ string, listener chan spec.BlockchainEvent, includeTx bool) governor.Service {
	c := tipChaser{
		coreAddrZMQ: coreAddrZMQ,
		listener:    listener,
		includeTx:   includeTx,
	}
	return &c
}

type tipChaser struct {
	governor.ServiceCtx
	coreAddrZMQ string
	listener    chan spec.BlockchainEvent
	includeTx   bool
}

func (c *tipChaser) Run() {
	// Connect to Core
	sock := c.connectZMQ(c.includeTx)
	if sock == nil {
		return // stopping
	}
	lastid := []byte{}
	for !c.Stopping() {
		msg, err := sock.RecvMessageBytes(0)
		if err != nil {
			switch err := err.(type) {
			case zmq4.Errno:
				if err == zmq4.Errno(syscall.ETIMEDOUT) {
					// handle timeouts by looping again
					if c.Sleep(ERROR_DELAY) {
						return // stopping
					}
					continue
				} else if err == zmq4.Errno(syscall.EAGAIN) {
					continue
				} else {
					// handle other ZeroMQ error codes
					log.Printf("TipChaser: ZMQ err: %s", err)
					if c.Sleep(ERROR_DELAY) {
						return // stopping
					}
					continue
				}
			default:
				// handle other Go errors
				log.Printf("TipChaser: ZMQ err: %s", err)
				if c.Sleep(ERROR_DELAY) {
					return // stopping
				}
				continue
			}
		}
		tag := string(msg[0])
		switch tag {
		case "hashblock":
			blockid := msg[1]
			if !bytes.Equal(blockid, lastid) {
				lastid = blockid
				c.listener <- spec.BlockchainEvent{Event: spec.EventTypeBlock, Hash: blockid}
			}
		case "hashtx":
			if c.includeTx {
				txid := msg[1]
				c.listener <- spec.BlockchainEvent{Event: spec.EventTypeTx, Hash: txid}
			}
		default:
		}
	}
}

func (c *tipChaser) connectZMQ(needTx bool) *zmq4.Socket {
	for {
		if c.Stopping() {
			return nil // stopping
		}
		sock, err := zmq4.NewSocket(zmq4.SUB)
		if err != nil {
			log.Printf("TipChaser: cannot create ZMQ socket")
			if c.Sleep(RETRY_DELAY) {
				return nil // stopping
			}
			continue
		}
		sock.SetRcvtimeo(2 * time.Second) // for shutdown
		err = sock.Connect(c.coreAddrZMQ)
		if err != nil {
			log.Printf("TipChaser: cannot connect to Core (%v): %v", c.coreAddrZMQ, err)
			if c.Sleep(RETRY_DELAY) {
				return nil // stopping
			}
			continue
		}
		err = sock.SetSubscribe("hashblock")
		if err != nil {
			log.Printf("TipChaser: cannot subscribe to 'hashblock': %v", err)
			if c.Sleep(RETRY_DELAY) {
				return nil // stopping
			}
			continue
		}
		if needTx {
			err = sock.SetSubscribe("hashtx")
			if err != nil {
				log.Printf("TipChaser: cannot subscribe to 'hashtx': %v", err)
				if c.Sleep(RETRY_DELAY) {
					return nil // stopping
				}
			}
		}
		return sock // success
	}
}
