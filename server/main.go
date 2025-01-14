package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/pkg/chaser"
	"github.com/dogeorg/dogewalker/pkg/core"
	"github.com/dogeorg/dogewalker/pkg/walker"
)

type Config struct {
	rpcHost   string
	rpcPort   int
	rpcUser   string
	rpcPass   string
	zmqHost   string
	zmqPort   int
	batchSize int
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

	ctx, shutdown := context.WithCancel(context.Background())

	// Core Node blockchain access.
	blockchain := core.NewCoreRPCClient(config.rpcHost, config.rpcPort, config.rpcUser, config.rpcPass)

	// Watch for new blocks.
	zmqTip, err := core.CoreZMQListener(ctx, config.zmqHost, config.zmqPort)
	if err != nil {
		log.Printf("CoreZMQListener: %v", err)
		os.Exit(1)
	}
	tipChanged := chaser.NewTipChaser(ctx, zmqTip, blockchain).Listen(1, true)

	// Walk the blockchain.
	blocks, err := walker.WalkTheDoge(ctx, walker.WalkerOptions{
		Chain:           &doge.DogeMainNetChain,
		ResumeFromBlock: "0e0bd6be24f5f426a505694bf46f60301a3a08dfdfda13854fdfe0ce7d455d6f",
		Client:          blockchain,
		TipChanged:      tipChanged,
	})
	if err != nil {
		log.Printf("WalkTheDoge: %v", err)
		os.Exit(1)
	}

	// Log new blocks.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-blocks:
				if b.Block != nil {
					log.Printf("block: %v (%v)", b.Block.Hash, b.Block.Height)
				} else {
					log.Printf("undo to: %v (%v)", b.Undo.ResumeFromBlock, b.Undo.LastValidHeight)
				}
			}
		}
	}()

	// Hook ^C signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for {
			select {
			case sig := <-sigCh: // sigterm/sigint caught
				log.Printf("Caught %v signal, shutting down", sig)
				shutdown()
				continue
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for shutdown.
	<-ctx.Done()
}
