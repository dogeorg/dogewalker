package walker

import (
	"log"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/spec"
	"github.com/dogeorg/governor"
)

const (
	RETRY_DELAY   = 5 * time.Second  // for RPC and Database errors.
	POLL_INTERVAL = 60 * time.Second // average time between blocks.
	POLL_WAITING  = 10 * time.Second // polling interval when a block is due.
	POLL_FALLBACK = 90 * time.Second // fallback interval when using TipChaser.
)

// The type of the DogeWalker output channel; either block, undo or idle.
type BlockOrUndo struct {
	ResumeFromBlock string          // the `ResumeFromBlock` hash to restart from (always)
	Height          int64           // the new block height (after this message is processed)
	Block           *ChainBlock     // either the next block in the chain
	Undo            *UndoForkBlocks // or an undo event (roll back blocks on a fork)
	Idle            bool            // or "idle" meaning we're at the tip of the blockchain
}

// NextBlock represents the next block in the blockchain.
type ChainBlock struct {
	Hash   string     // hash of the block
	Height int64      // height of the block
	Block  doge.Block // decoded block header and transactions
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
	Chain           *doge.ChainParams // chain parameters, e.g. doge.DogeMainNetChain
	ResumeFromBlock string            // last processed block hash to begin walking from (hex)
	Client          spec.Blockchain   // from NewCoreRPCClient()
	TipChanged      chan string       // from TipChaser()
	FullUndoBlocks  bool              // fully decode blocks in UndoForkBlocks (or just hash and height)
	BufferBlocks    int               // number of blocks to decode ahead of the consumer (channel size, default 10)
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
 * `ResumeFromBlock`: pass the last block hash you have processed
 * (e.g. stored in your database) to start from the beginning of the
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
		tipChanged:     opts.TipChanged,
		fullUndoBlocks: opts.FullUndoBlocks,
		resumeFrom:     opts.ResumeFromBlock,
		blockInterval:  POLL_INTERVAL,
	}
	if opts.TipChanged != nil {
		// We will receive tipChanged notifications: use a longer polling timer
		// as a fallback in case the tipChanged source stops working.
		c.blockInterval = POLL_FALLBACK
	}
	return &c, c.output
}

// dogeWalker is the internal state.
type dogeWalker struct {
	governor.ServiceCtx
	output         chan BlockOrUndo
	client         spec.Blockchain
	chain          *doge.ChainParams
	tipChanged     chan string     // receive from TipChaser.
	stop           <-chan struct{} // ctx.Done() channel.
	fullUndoBlocks bool            // fully decode blocks in UndoForkBlocks
	resumeFrom     string          // last processed block hash to begin walking from (hex)
	blockInterval  time.Duration   // interval for polling blocks (longer if tipChanged is set)
	isIdle         bool            // true if the last message we sent was 'idle'
}

func (c *dogeWalker) Run() {
	c.stop = c.Context.Done()
	// Check that Core is following the same chain we want to follow.
	genesisHash, cont := c.fetchBlockHash(0)
	if !cont {
		return // stopping
	}
	if genesisHash != c.chain.GenesisBlock {
		log.Printf("DogeWalker: WRONG CHAIN! Expected chain '%s' but Core Node does not have a matching Genesis Block hash, it has %s", c.chain.ChainName, genesisHash)
		if c.Sleep(60 * time.Second) {
			return // stopping
		}
		return // service will restart
	}
	// Start from the specified block, or the start of the blockchain.
	resumeFrom := c.resumeFrom
	if resumeFrom == "" {
		log.Printf("DogeWalker: no resume-from block hash: starting from genesis block: %v", resumeFrom)
		resumeFrom, cont = c.followTheChain(int64(0), genesisHash)
		if !cont {
			return // stopping
		}
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
		// Get the last-processed block header (the restart-point)
		head, cont := c.fetchBlockHeader(resumeFrom)
		if !cont {
			return // stopping
		}
		nextBlockHash := head.NextBlockHash // can be ""
		if head.Confirmations == -1 {
			// Last-processed block is longer on-chain, start with a rollback.
			undo, nextBlock, cont := c.undoBlocks(head)
			if !cont {
				return // stopping
			}
			c.output <- BlockOrUndo{Undo: undo, ResumeFromBlock: undo.LastValidHash, Height: undo.LastValidHeight}
			resumeFrom = undo.LastValidHash // we reverted some blocks
			nextBlockHash = nextBlock       // can be ""
		}

		// Follow the Blockchain to the Tip.
		// nextBlockHash can safely be "" on entry.
		lastProcessed, cont := c.followTheChain(head.Height, nextBlockHash)
		if !cont {
			return // stopping
		}
		if lastProcessed != "" {
			// Made forward progress, or found a rollback.
			resumeFrom = lastProcessed
			timerInterval = c.blockInterval // reset polling interval
		}

		// Wait for Core to signal a new Best Block (new block mined)
		// or a shutdown request from Governor.
		timerDrained = resetTimer(timer, timerInterval, timerDrained)
		select {
		case <-c.stop:
			return // stopping
		case <-c.tipChanged: // ignored if tipChanged is nil
			log.Println("DogeWalker: received tip-change")
		case <-timer.C:
			timerDrained = true
			timerInterval = POLL_WAITING // shorten until the next block is found
			log.Println("DogeWalker: polling for the next block")
		}
	}
}

func (c *dogeWalker) followTheChain(height int64, nextBlockHash string) (lastProcessed string, running bool) {
	// Follow the chain forwards from a new block, nextBlockHash.
	// If we encounter a fork, generate an Undo.
	for nextBlockHash != "" {
		if c.Stopping() {
			return "", false // stopping
		}
		head, cont := c.fetchBlockHeader(nextBlockHash)
		if !cont {
			return "", false // stopping
		}
		if head.Confirmations != -1 {
			// This block is still on-chain.
			// Output the decoded block.
			blockData, cont := c.fetchBlockData(head.Hash)
			if !cont {
				return "", false // stopping
			}
			block, valid := doge.DecodeBlock(blockData, true)
			if !valid {
				log.Printf("DogeWalker: INVALID BLOCK! cannot parse '%s' at height %v", head.Hash, head.Height)
				return lastProcessed, true // will resume at lastProcessed (and retry this block)
			}
			cb := &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
				Block:  block,
			}
			c.output <- BlockOrUndo{Block: cb, ResumeFromBlock: head.Hash, Height: head.Height}
			lastProcessed = head.Hash // we made forward progress
			nextBlockHash = head.NextBlockHash
			height = head.Height
			c.isIdle = false
		} else {
			// This block is no longer on-chain.
			// Roll back until we find a block that is on-chain.
			undo, nextBlock, cont := c.undoBlocks(head)
			if !cont {
				return lastProcessed, false
			}
			c.output <- BlockOrUndo{Undo: undo, ResumeFromBlock: undo.LastValidHash, Height: undo.LastValidHeight}
			lastProcessed = undo.LastValidHash // we reverted some blocks
			nextBlockHash = nextBlock
			height = undo.LastValidHeight
			c.isIdle = false
		}
	}
	if !c.isIdle {
		c.output <- BlockOrUndo{Idle: true, ResumeFromBlock: lastProcessed, Height: height}
		c.isIdle = true
	}
	return lastProcessed, true
}

func (c *dogeWalker) undoBlocks(head spec.BlockHeader) (undo *UndoForkBlocks, nextBlockHash string, running bool) {
	// Walk backwards along the chain (in Core) to find an on-chain block.
	undo = &UndoForkBlocks{}
	for head.Confirmations != -1 {
		if c.Stopping() {
			return undo, "", false // stopping
		}
		// Accumulate undo info.
		undo.UndoBlocks = append(undo.UndoBlocks, head.Hash)
		if c.fullUndoBlocks {
			blockData, cont := c.fetchBlockData(head.Hash)
			if !cont {
				return undo, "", false // stopping
			}
			block, valid := doge.DecodeBlock(blockData, true)
			if !valid {
				log.Printf("DogeWalker: INVALID BLOCK during UNDO! cannot parse '%s' at height %v", head.Hash, head.Height)
			}
			undo.FullBlocks = append(undo.FullBlocks, &ChainBlock{
				Hash:   head.Hash,
				Height: head.Height,
				Block:  block,
			})
		}
		// Fetch the block header for the previous block.
		cont := true
		head, cont = c.fetchBlockHeader(head.PreviousBlockHash)
		if !cont {
			return undo, "", false // stopping
		}
	}
	// Found an on-chain block: stop rolling back.
	undo.LastValidHeight = head.Height
	undo.LastValidHash = head.Hash
	return undo, head.NextBlockHash, true
}

func (c *dogeWalker) fetchBlockData(blockHash string) ([]byte, bool) {
	for {
		hex, err := c.client.GetBlock(blockHash)
		if err != nil {
			log.Println("DogeWalker: error retrieving block (will retry):", err)
			if c.Sleep(RETRY_DELAY) {
				return []byte{}, false // stopping
			}
		} else {
			bytes, err := doge.HexDecode(hex)
			if err != nil {
				log.Println("DogeWalker: invalid block hex (will retry):", err)
				if c.Sleep(RETRY_DELAY) {
					return []byte{}, false // stopping
				}
			} else {
				return bytes, true
			}
		}
	}
}

func (c *dogeWalker) fetchBlockHeader(blockHash string) (spec.BlockHeader, bool) {
	for {
		block, err := c.client.GetBlockHeader(blockHash)
		if err != nil {
			log.Println("DogeWalker: error retrieving block header (will retry):", err)
			if c.Sleep(RETRY_DELAY) {
				return block, false // stopping
			}
		} else {
			return block, true
		}
	}
}

func (c *dogeWalker) fetchBlockHash(height int64) (string, bool) {
	for {
		hash, err := c.client.GetBlockHash(height)
		if err != nil {
			log.Println("DogeWalker: error retrieving block hash (will retry):", err)
			if c.Sleep(RETRY_DELAY) {
				return "", false // stopping
			}
		} else {
			return hash, true
		}
	}
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
