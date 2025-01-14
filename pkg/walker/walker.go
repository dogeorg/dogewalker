package walker

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/pkg/spec"
)

const (
	RETRY_DELAY = 5 * time.Second // for RPC and Database errors.
)

// The type of the DogeWalker output channel; either block or undo
type BlockOrUndo struct {
	Block *ChainBlock     // either the next block in the chain
	Undo  *UndoForkBlocks // or an undo event (roll back blocks on a fork)
}

// NextBlock represents the next block in the blockchain.
type ChainBlock struct {
	Hash   string
	Height int64
	Block  doge.Block
}

// UndoForkBlocks represents a Fork in the Blockchain: blocks to undo on the off-chain fork
type UndoForkBlocks struct {
	LastValidHeight int64         // undo all blocks greater than this height
	ResumeFromBlock string        // hash of last valid on-chain block (to resume on restart)
	BlockHashes     []string      // hashes of blocks to be undone
	FullBlocks      []*ChainBlock // present if FullUndoBlocks is true in WalkerOptions
}

// Configuraton for WalkTheDoge.
type WalkerOptions struct {
	Chain           *doge.ChainParams // chain parameters, e.g. doge.DogeMainNetChain
	ResumeFromBlock string            // last processed block hash to begin walking from (hex)
	Client          spec.Blockchain   // from NewCoreRPCClient()
	TipChanged      chan string       // from TipChaser()
	FullUndoBlocks  bool              // fully decode blocks in UndoForkBlocks (or just hash and height)
}

// Private WalkTheDoge internal state.
type DogeWalker struct {
	output         chan BlockOrUndo
	client         spec.Blockchain
	chain          *doge.ChainParams
	tipChanged     chan string     // receive from TipChaser.
	stop           <-chan struct{} // ctx.Done() channel.
	stopping       bool            // set to exit the main loop.
	fullUndoBlocks bool            // fully decode blocks in UndoForkBlocks
}

/*
 * WalkTheDoge walks the blockchain, keeping up with the Tip (Best Block)
 *
 * It outputs decoded blocks to the returned 'blocks' channel.
 *
 * If there's a reorganisation (fork), it will walk backwards to the
 * fork-point, building a list of blocks to undo, until it finds a block
 * that's still on the main chain. Then it will output UndoForkBlocks
 * to allow you to undo any data in your systems related to those blocks.
 *
 * Note: when you undo blocks, you will need to restore any UTXOs spent
 * by those blocks (spending blocks don't contain enough information to
 * re-create the spent UTXOs, so you must keep them for e.g. 100 blocks)
 *
 * fullUndoBlocks: pass fully decoded blocks to the UndoForkBlocks callback.
 * Useful if you want to manually undo each transaction, rather than undoing
 * everything above `LastValidHeight` by tagging data with block-heights.
 */
func WalkTheDoge(ctx context.Context, opts WalkerOptions) (blocks chan BlockOrUndo, err error) {
	c := DogeWalker{
		// The larger this channel is, the more blocks we can decode-ahead.
		output:         make(chan BlockOrUndo, 100),
		client:         opts.Client,
		chain:          opts.Chain,
		tipChanged:     opts.TipChanged,
		stop:           ctx.Done(),
		fullUndoBlocks: opts.FullUndoBlocks,
	}
	err = c.verifyChain()
	if err != nil {
		return nil, err // bad config
	}
	go func() {
		// Recover from panic used to stop the service.
		// We use this to avoid returning a 'stopping' bool from every single function.
		defer func() {
			if r := recover(); r != nil {
				log.Println("DogeWalker: panic received:", r)
			}
		}()
		resumeFromBlock := opts.ResumeFromBlock
		if resumeFromBlock == "" {
			resumeFromBlock = c.fetchBlockHash(1)
			log.Printf("DogeWalker: no resume-from block hash: starting from origin block: %v", resumeFromBlock)
		}
		for {
			// Get the last-processed block header (restart point)
			head := c.fetchBlockHeader(resumeFromBlock)
			nextBlockHash := head.NextBlockHash // can be ""
			if head.Confirmations == -1 {
				// No longer on-chain, start with a rollback.
				undo, nextBlock := c.undoBlocks(head)
				c.output <- BlockOrUndo{Undo: undo}
				nextBlockHash = nextBlock // can be ""
			}

			// Follow the Blockchain to the Tip.
			lastProcessed := c.followTheChain(nextBlockHash)
			if lastProcessed != "" {
				resumeFromBlock = lastProcessed
			}

			// Wait for Core to signal a new Best Block (new block mined)
			// or a Command to arrive.
			select {
			case <-c.stop:
				log.Println("DogeWalker: received stop signal")
				c.stopping = true
				return
			case <-c.tipChanged:
				log.Println("DogeWalker: received new block signal")
			}
		}
	}()
	return c.output, nil
}

func (c *DogeWalker) verifyChain() error {
	genesisHash := c.fetchBlockHash(0)
	if genesisHash != c.chain.GenesisBlock {
		return fmt.Errorf("WRONG CHAIN! Expected chain '%s' but Core Node does not have a matching Genesis Block hash, it has %s", c.chain.ChainName, genesisHash)
	}
	return nil
}

func (c *DogeWalker) followTheChain(nextBlockHash string) (lastProcessed string) {
	// Follow the chain forwards.
	// If we encounter a fork, generate an Undo.
	for nextBlockHash != "" {
		head := c.fetchBlockHeader(nextBlockHash)
		if head.Confirmations != -1 {
			// This block is still on-chain.
			// Output the decoded block.
			blockData := c.fetchBlockData(head.Hash)
			block := &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
				Block:  doge.DecodeBlock(blockData),
			}
			c.output <- BlockOrUndo{Block: block}
			lastProcessed = block.Hash
			nextBlockHash = head.NextBlockHash
		} else {
			// This block is no longer on-chain.
			// Roll back until we find a block that is on-chain.
			undo, nextBlock := c.undoBlocks(head)
			c.output <- BlockOrUndo{Undo: undo}
			lastProcessed = undo.ResumeFromBlock
			nextBlockHash = nextBlock
		}
		c.checkShutdown() // loops must check for shutdown.
	}
	return
}

func (c *DogeWalker) undoBlocks(head spec.BlockHeader) (undo *UndoForkBlocks, nextBlockHash string) {
	// Walk backwards along the chain (in Core) to find an on-chain block.
	undo = &UndoForkBlocks{}
	for {
		// Accumulate undo info.
		undo.BlockHashes = append(undo.BlockHashes, head.Hash)
		if c.fullUndoBlocks {
			blockData := c.fetchBlockData(head.Hash)
			undo.FullBlocks = append(undo.FullBlocks, &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
				Block:  doge.DecodeBlock(blockData),
			})
		}
		// Fetch the block header for the previous block.
		head = c.fetchBlockHeader(head.PreviousBlockHash)
		if head.Confirmations == -1 {
			// This block is no longer on-chain; keep walking backwards.
			c.checkShutdown() // loops must check for shutdown.
			continue
		} else {
			// Found an on-chain block: stop rolling back.
			undo.LastValidHeight = head.Height
			undo.ResumeFromBlock = head.Hash
			return undo, head.NextBlockHash
		}
	}
}

func (c *DogeWalker) fetchBlockData(blockHash string) []byte {
	for {
		hex, err := c.client.GetBlock(blockHash)
		if err != nil {
			log.Println("ChainWalker: error retrieving block (will retry):", err)
			c.sleepForRetry(0)
		} else {
			bytes, err := doge.HexDecode(hex)
			if err != nil {
				log.Println("ChainWalker: invalid block hex (will retry):", err)
				c.sleepForRetry(0)
			}
			return bytes
		}
	}
}

func (c *DogeWalker) fetchBlockHeader(blockHash string) spec.BlockHeader {
	for {
		block, err := c.client.GetBlockHeader(blockHash)
		if err != nil {
			log.Println("ChainWalker: error retrieving block header (will retry):", err)
			c.sleepForRetry(0)
		} else {
			return block
		}
	}
}

func (c *DogeWalker) fetchBlockHash(height int64) string {
	for {
		hash, err := c.client.GetBlockHash(height)
		if err != nil {
			log.Println("ChainWalker: error retrieving block hash (will retry):", err)
			c.sleepForRetry(0)
		} else {
			return hash
		}
	}
}

// func (c *DogeWalker) fetchBlockCount() int64 {
// 	for {
// 		count, err := c.client.GetBlockCount()
// 		if err != nil {
// 			log.Println("ChainWalker: error retrieving block count (will retry):", err)
// 			c.sleepForRetry(0)
// 		} else {
// 			return count
// 		}
// 	}
// }

func (c *DogeWalker) sleepForRetry(delay time.Duration) {
	if delay == 0 {
		delay = RETRY_DELAY
	}
	select {
	case <-c.stop:
		log.Println("ChainWalker: received stop signal")
		c.stopping = true
		panic("stopped") // caught in `Run` method.
	case <-time.After(delay):
		return
	}
}

func (c *DogeWalker) checkShutdown() {
	select {
	case <-c.stop:
		log.Println("ChainWalker: received stop signal")
		c.stopping = true
		panic("stopped") // caught in `Run` method.
	default:
		return
	}
}

// chainFromName returns ChainParams for: 'main', 'test', 'regtest'.
func ChainFromName(chainName string) (*doge.ChainParams, error) {
	switch chainName {
	case "main":
		return &doge.DogeMainNetChain, nil
	case "test":
		return &doge.DogeTestNetChain, nil
	case "regtest":
		return &doge.DogeRegTestChain, nil
	default:
		return &doge.ChainParams{}, fmt.Errorf("unknown chain: %v", chainName)
	}
}
