package walker

import (
	"context"
	"log"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/v2/core"
	"github.com/dogeorg/dogewalker/v2/spec"
	"github.com/dogeorg/governor"
)

const (
	WALKER_RETRY_COUNT = 0                // keep trying until the context is cancelled.
	WALKER_RETRY_DELAY = 5 * time.Second  // for RPC retries.
	POLL_INTERVAL      = 60 * time.Second // average time between blocks.
	POLL_WAITING       = 10 * time.Second // polling interval when a block is due.
	POLL_FALLBACK      = 90 * time.Second // fallback interval when using TipChaser.
)

// The type of the DogeWalker output channel; either block, undo or idle.
type BlockOrUndo struct {
	LastProcessedBlock string          // the `LastProcessedBlock` hash to resume from (always)
	Height             int64           // the new block height (after this message is processed)
	Block              *ChainBlock     // either the next block in the chain
	Undo               *UndoForkBlocks // or an undo event (roll back blocks on a fork)
	Idle               bool            // or "idle" meaning we're at the tip of the blockchain
}

type BlockMetadata struct {
	MedianTime uint64  // The median block time in seconds since UNIX epoch (Jan 1 1970 GMT)
	Difficulty float64 // The difficulty
	ChainWork  string  // Expected number of hashes required to produce the chain up to this block (hex)
	BlockSize  int     // The size of the block in bytes
}

// NextBlock represents the next block in the blockchain.
type ChainBlock struct {
	Hash     string        // hash of the block
	Height   int64         // height of the block
	Block    doge.Block    // decoded block header and transactions
	Metadata BlockMetadata // block metadata from Core Node
}

// UndoForkBlocks represents a Fork in the Blockchain: blocks to undo on the off-chain fork
type UndoForkBlocks struct {
	LastValidHeight int64         // undo all blocks greater than this height
	LastValidHash   string        // hash of the last on-chain block that's still valid
	UndoBlocks      []string      // hashes of blocks to be undone
	FullBlocks      []*ChainBlock // optional: present if FullUndoBlocks is true in WalkerOptions
}

// Configuraton for WalkTheDoge.
type WalkerOptions struct {
	Chain              *doge.ChainParams           // chain parameters, e.g. doge.DogeMainNetChain
	LastProcessedBlock string                      // last processed block hash to begin walking from (hex)
	Client             spec.Blockchain             // from NewCoreRPCClient()
	ChainEvents        <-chan spec.BlockchainEvent // from TipChaser()
	FullUndoBlocks     bool                        // fully decode blocks in UndoForkBlocks (or just hash and height)
	BufferBlocks       int                         // number of blocks to decode ahead of the consumer (channel size, default 10)
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
 * `Chain`: a ChainParams instance containing the GenesisBlock hash.
 * e.g. doge.DogeMainNetChain or use `walker.ChainFromName`
 *
 * `LastProcessedBlock`: the last block hash you have processed
 * (i.e. stored in your database.) To start from the beginning of the
 * chain, pass "". Alternatively use `FindTheTip` to find a block
 * at or near the current tip of the blockchain.
 *
 * `FullUndoBlocks`: pass fully decoded blocks to the UndoForkBlocks callback.
 * Useful if you want to manually undo each transaction, rather than undoing
 * everything above `LastValidHeight` by tagging your data with block-heights.
 *
 * `TipChanged`: optional `chan string` to notify WalkTheDoge that a new block
 * has been broadcast on the Dogecoin network. e.g. `core.NewTipChaser`
 * If this is nil, WalkTheDoge will use a timer to poll Core.
 */
func WalkTheDoge(opts WalkerOptions) (service governor.Service, blocks chan BlockOrUndo) {
	chanSize := opts.BufferBlocks
	if chanSize <= 0 {
		chanSize = 10 // default size
	}
	if opts.Chain == nil {
		panic("WalkTheDoge: `chain` cannot be nil, e.g. doge.DogeMainNetChain")
	}
	if opts.Client == nil {
		panic("WalkTheDoge: `client` cannot be nil, e.g. core.NewCoreRPCClient")
	}
	c := dogeWalker{
		// The larger this channel is, the more blocks we can decode-ahead.
		output:         make(chan BlockOrUndo, chanSize),
		client:         opts.Client,
		chain:          opts.Chain,
		chainEvents:    opts.ChainEvents,
		fullUndoBlocks: opts.FullUndoBlocks,
		lastProcessed:  opts.LastProcessedBlock,
		blockInterval:  POLL_INTERVAL,
	}
	if opts.ChainEvents != nil {
		// We will receive tipChanged notifications: use a longer polling timer
		// as a fallback in case the tipChanged source stops working.
		c.blockInterval = POLL_FALLBACK
	}
	return &c, c.output
}

// dogeWalker is the internal state.
type dogeWalker struct {
	governor.ServiceCtx
	rpcContext     context.Context
	output         chan BlockOrUndo
	client         spec.Blockchain
	chain          *doge.ChainParams
	chainEvents    <-chan spec.BlockchainEvent // receive from TipChaser.
	tipChanged     <-chan bool                 // from watchForTipChanges().
	stop           <-chan struct{}             // ctx.Done() channel.
	fullUndoBlocks bool                        // fully decode blocks in UndoForkBlocks
	lastProcessed  string                      // last processed block hash to begin walking from (hex)
	blockInterval  time.Duration               // interval for polling blocks (longer if tipChanged is set)
	isIdle         bool                        // true if the last message we sent was 'idle'
}

func (c *dogeWalker) Run() {
	c.stop = c.Context.Done()
	c.rpcContext = core.ContextWithCoreRPCRetry(c.Context, WALKER_RETRY_COUNT, WALKER_RETRY_DELAY, "DogeWalker")
	c.tipChanged = watchForTipChanges(c.stop, c.chainEvents)
	lastProcessed, cont := c.findLastProcessedBlock()
	if !cont {
		return // stopping
	}
	// Polling timer to discover new blocks.
	// This operates as a fallback if we're using TipChaser via the TipChanged channel.
	timerInterval := c.blockInterval
	timerDrained := false
	timer := time.NewTimer(timerInterval)
	defer func() {
		if !timer.Stop() {
			<-timer.C // drain the channel for GC
		}
	}()
	// Follow the chain until the service stops
	for !c.Stopping() {
		newLastProcessed, cont := c.checkForNewBlocks(lastProcessed)
		if !cont {
			return // stopping
		}
		if newLastProcessed != lastProcessed {
			lastProcessed = newLastProcessed
			timerInterval = c.blockInterval // reset polling interval
		}
		// Wait for Core to signal a new Best Block (new block mined)
		// or a shutdown request from Governor.
		timerDrained = resetTimer(timer, timerInterval, timerDrained)
		select {
		case <-c.stop:
			return // stopping
		case <-c.tipChanged:
			log.Println("DogeWalker: received tip-change")
		case <-timer.C:
			timerDrained = true
			timerInterval = POLL_WAITING // shorten until the next block is found
			log.Println("DogeWalker: polling for the next block")
		}
	}
}

func (c *dogeWalker) findLastProcessedBlock() (lastProcessed string, running bool) {
	// Check that Core is following the same chain we want to follow.
	genesisHash, err := c.client.GetBlockHash(c.rpcContext, 0)
	if err != nil {
		return "", false // stopping (report/retry handled by c.client)
	}
	if genesisHash != c.chain.GenesisBlock {
		log.Printf("[DogeWalker] WRONG CHAIN! Expected chain '%s' but Core Node does not have a matching Genesis Block hash, it has %s", c.chain.ChainName, genesisHash)
		c.Sleep(60 * time.Second)
		return "", false // service will restart
	}
	// Start from the specified block, or the start of the blockchain.
	lastProcessed = c.lastProcessed
	if lastProcessed == "" {
		log.Printf("[DogeWalker] no resume-from block hash: starting from genesis block: %v", lastProcessed)
		cont := true
		lastProcessed, cont = c.processGenesisBlock()
		if !cont {
			return "", false // stopping (report/retry handled by c.client)
		}
	}
	return lastProcessed, true
}

func (c *dogeWalker) checkForNewBlocks(lastProcessed string) (newLastProcessed string, running bool) {
	// Get the last-processed block header (the restart-point)
	head, err := c.client.GetBlockHeader(c.rpcContext, lastProcessed)
	if err != nil {
		return "", false // stopping (report/retry handled by c.client)
	}
	nextBlockHash := head.NextBlockHash // can be ""
	nextHeight := head.Height
	if head.Confirmations == -1 {
		// Last-processed block is longer on-chain, start with a rollback.
		undo, nextBlock, cont := c.undoBlocks(head)
		if !cont {
			return "", false // stopping
		}
		select {
		case c.output <- BlockOrUndo{Undo: undo, LastProcessedBlock: undo.LastValidHash, Height: undo.LastValidHeight}:
			break
		case <-c.stop:
			return "", false // stopping
		}

		lastProcessed = undo.LastValidHash // we reverted some blocks
		nextBlockHash = nextBlock          // can be ""
		nextHeight = undo.LastValidHeight
	}

	// Follow the Blockchain to the Tip.
	// nextBlockHash can safely be "" on entry.
	justProcessed, cont := c.followTheChain(nextHeight, nextBlockHash)
	if !cont {
		return "", false // stopping
	}
	if justProcessed != "" {
		// Made forward progress, or found a rollback.
		lastProcessed = justProcessed
	}
	return lastProcessed, true
}

func (c *dogeWalker) followTheChain(height int64, nextUnprocessed string) (lastProcessed string, running bool) {
	// Follow the chain forwards from a new block, nextUnprocessed.
	// If we encounter a fork, generate an Undo.
	for nextUnprocessed != "" {
		if c.Stopping() {
			return "", false // stopping
		}
		head, err := c.client.GetBlockHeader(c.rpcContext, nextUnprocessed)
		if err != nil {
			return "", false // stopping (report/retry handled by c.client)
		}
		if head.Confirmations != -1 {
			// This block is still on-chain.
			// Output the decoded block.
			block, size, err := c.client.GetBlock(c.rpcContext, head.Hash)
			if err != nil {
				return "", false // stopping (report/retry handled by c.client)
			}
			cb := &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
				Block:  block,
				Metadata: BlockMetadata{
					MedianTime: head.MedianTime,
					Difficulty: head.Difficulty,
					ChainWork:  head.ChainWork,
					BlockSize:  size,
				},
			}
			select {
			case c.output <- BlockOrUndo{Block: cb, LastProcessedBlock: head.Hash, Height: head.Height}:
				break
			case <-c.stop:
				return "", false // stopping
			}
			lastProcessed = head.Hash // we made forward progress
			nextUnprocessed = head.NextBlockHash
			height = head.Height
			c.isIdle = false
		} else {
			// This block is no longer on-chain.
			// Check if core is performing initial sync, or re-indexing.
			stopping := c.client.WaitForSync(c.rpcContext)
			if stopping {
				return "", false // stopping
			}
			// Re-fetch the block header to check if it's still off-chain.
			head, err := c.client.GetBlockHeader(c.rpcContext, nextUnprocessed)
			if err != nil {
				return "", false // stopping (report/retry handled by c.client)
			}
			if head.Confirmations != -1 {
				continue // block is now on-chain, go back and retry.
			}
			// Roll back until we find a block that is on-chain.
			undo, nextBlock, cont := c.undoBlocks(head)
			if !cont {
				return lastProcessed, false
			}
			select {
			case c.output <- BlockOrUndo{Undo: undo, LastProcessedBlock: undo.LastValidHash, Height: undo.LastValidHeight}:
				break
			case <-c.stop:
				return "", false // stopping
			}
			lastProcessed = undo.LastValidHash // we reverted some blocks
			nextUnprocessed = nextBlock
			height = undo.LastValidHeight
			c.isIdle = false
		}
	}
	if !c.isIdle {
		select {
		case c.output <- BlockOrUndo{Idle: true, LastProcessedBlock: lastProcessed, Height: height}:
			break
		case <-c.stop:
			return "", false // stopping
		}
		c.isIdle = true
	}
	return lastProcessed, true
}

func (c *dogeWalker) undoBlocks(head spec.BlockHeader) (undo *UndoForkBlocks, nextBlockHash string, running bool) {
	// Walk backwards along the chain (in Core) to find an on-chain block.
	undo = &UndoForkBlocks{}
	for head.Confirmations == -1 {
		if c.Stopping() {
			return undo, "", false // stopping
		}
		// Accumulate undo info.
		undo.UndoBlocks = append(undo.UndoBlocks, head.Hash)
		if c.fullUndoBlocks {
			block, size, err := c.client.GetBlock(c.rpcContext, head.Hash)
			if err != nil {
				return undo, "", false // stopping (report/retry handled by c.client)
			}
			undo.FullBlocks = append(undo.FullBlocks, &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
				Block:  block,
				Metadata: BlockMetadata{
					MedianTime: head.MedianTime,
					Difficulty: head.Difficulty,
					ChainWork:  head.ChainWork,
					BlockSize:  size,
				},
			})
		}
		// Fetch the block header for the previous block.
		var err error
		head, err = c.client.GetBlockHeader(c.rpcContext, head.PreviousBlockHash)
		if err != nil {
			return undo, "", false // stopping (report/retry handled by c.client)
		}
	}
	// Found an on-chain block: stop rolling back.
	undo.LastValidHeight = head.Height
	undo.LastValidHash = head.Hash
	return undo, head.NextBlockHash, true
}

func (c *dogeWalker) processGenesisBlock() (lastProcessed string, running bool) {
	for !c.Stopping() {
		head, err := c.client.GetBlockHeader(c.rpcContext, c.chain.GenesisBlock)
		if err != nil {
			return "", false // stopping (report/retry handled by c.client)
		}
		if head.Confirmations == -1 {
			log.Printf("[DogeWalker] genesis block is not on-chain! waiting for sync...")
			stopping := c.client.WaitForSync(c.rpcContext)
			if stopping {
				return "", false // stopping
			}
			c.Sleep(WALKER_RETRY_DELAY)
			continue
		}
		block, size, err := c.client.GetBlock(c.rpcContext, head.Hash)
		if err != nil {
			return "", false // stopping (report/retry handled by c.client)
		}
		cb := &ChainBlock{
			Hash:   head.Hash,
			Height: head.Height,
			Block:  block,
			Metadata: BlockMetadata{
				MedianTime: head.MedianTime,
				Difficulty: head.Difficulty,
				ChainWork:  head.ChainWork,
				BlockSize:  size,
			},
		}
		select {
		case c.output <- BlockOrUndo{Block: cb, LastProcessedBlock: head.Hash, Height: head.Height}:
		case <-c.stop:
			return "", false // stopping
		}
		return head.Hash, true // success
	}
	return
}

func resetTimer(t *time.Timer, d time.Duration, isDrained bool) bool {
	// The semantics of Timer are rediculous: we can only receive from the
	// timer channel once (per Reset), but we cannot call Reset unless the
	// timer is stopped with the channel drained. Therefore we must track
	// the 'drained' state of the channel ourselves.
	// Stop "returns true if the call stops the timer, false if the timer has
	// already expired [triggered] or been stopped."
	// So: if the call didn't stop the timer, and we haven't drained the channel.
	if !t.Stop() && !isDrained {
		<-t.C // we must drain the channel
	}
	t.Reset(d)
	isDrained = false
	return isDrained
}

// Starts a goroutine to watch for tip changes.
// Returns a channel that signals when a tip change has occurred.
// This is used as a hint to wake up the main loop to check for new blocks.
func watchForTipChanges(stop <-chan struct{}, events <-chan spec.BlockchainEvent) (tipChanged <-chan bool) {
	changed := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-stop:
				return
			case e := <-events:
				// Ignore Tx events (we only care about block events).
				if e.Event == spec.EventTypeBlock {
					// Perform a non-blocking send.
					// We only need to know if a tip change has occurred.
					select {
					case changed <- true:
					default:
					}
				}
			}
		}
	}()
	return changed
}
