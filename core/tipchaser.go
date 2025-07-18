package core

import (
	"encoding/hex"
	"log"
	"syscall"
	"time"

	"github.com/dogeorg/governor"
	"github.com/pebbe/zmq4"
)

const RETRY_DELAY = 5 * time.Second // for connect errors.
const ERROR_DELAY = 1 * time.Second // for ZMQ errors.

/*
 * NewTipChaser listens to Core Node ZMQ interface.
 *
 * newTip channel announces whenever Core finds a new Best Block Hash (Tip change)
 *
 * `coreAddrZMQ` is the TCP address of Core ZMQ, e.g. "tcp://127.0.0.1:28332"
 */
func NewTipChaser(coreAddrZMQ string) (service governor.Service, newTip chan string) {
	newTip = make(chan string, 1)
	c := tipChaser{
		newTip:      newTip,
		coreAddrZMQ: coreAddrZMQ,
	}
	return &c, newTip
}

type tipChaser struct {
	governor.ServiceCtx
	newTip      chan string
	coreAddrZMQ string
}

func (c *tipChaser) Run() {
	// Connect to Core
	sock := c.connectZMQ()
	if sock == nil {
		return // stopping
	}
	lastid := ""
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
			blockid := hex.EncodeToString(msg[1])
			if blockid != lastid {
				lastid = blockid
				// Non-blocking send, because WalkTheDoge only needs a hint that a
				// new block has arrived (it doesn't need individual block-ids)
				select {
				case c.newTip <- blockid:
				default:
				}
			}

		default:
		}
	}
}

func (c *tipChaser) connectZMQ() *zmq4.Socket {
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
		return sock // success
	}
}
