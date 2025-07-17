package spec

import "github.com/dogeorg/doge"

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
	Client          Blockchain        // from NewCoreRPCClient()
	TipChanged      chan string       // from TipChaser()
	FullUndoBlocks  bool              // fully decode blocks in UndoForkBlocks (or just hash and height)
}
