package main

import (
	"fmt"
	"log"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/core"
	"github.com/dogeorg/dogewalker/walker"
	"github.com/dogeorg/governor"
)

type Config struct {
	rpcHost string
	rpcPort int
	rpcUser string
	rpcPass string
	zmqHost string
	zmqPort int
}

type LogBlocks struct {
	governor.ServiceCtx
	blocks chan walker.BlockOrUndo
}

func (l *LogBlocks) Run() {
	stop := l.Context.Done()
	for !l.Stopping() {
		select {
		case <-stop:
			return
		case b := <-l.blocks:
			if b.Block != nil {
				log.Printf("[%v] block: %v", b.Height, b.Block.Hash)
			} else if b.Undo != nil {
				log.Printf("[%v] undo to: %v", b.Height, b.Undo.LastValidHash)
			} else {
				log.Printf("[%v] idle: at the tip: %v", b.Height, b.ResumeFromBlock)
			}
		}
	}
}

func main() {
	config := Config{
		rpcHost: "127.0.0.1",
		rpcPort: 22555,
		rpcUser: "dogecoin",
		rpcPass: "dogecoin",
		zmqHost: "127.0.0.1",
		zmqPort: 28332,
	}

	gov := governor.New().CatchSignals().Restart(1 * time.Second)

	// Core Node blockchain access.
	blockchain := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// TipChaser
	zmqAddr := fmt.Sprintf("tcp://%v:%v", config.zmqHost, config.zmqPort)
	zmqSvc, tipChanged := core.NewTipChaser(zmqAddr)
	gov.Add("ZMQ", zmqSvc)

	// Get starting hash.
	fromBlock, _ := walker.FindTheTip(gov.GlobalContext(), blockchain, 100)

	// Walk the Doge.
	walkSvc, blocks := walker.WalkTheDoge(walker.WalkerOptions{
		Chain:           &doge.DogeMainNetChain,
		ResumeFromBlock: fromBlock,
		Client:          blockchain,
		TipChanged:      tipChanged,
	})
	gov.Add("Walk", walkSvc)

	// Log new blocks.
	gov.Add("Logger", &LogBlocks{blocks: blocks})

	// run services until interrupted.
	gov.Start()
	gov.WaitForShutdown()
	fmt.Println("finished.")
}
