package spec

import (
	"context"
	"errors"
	"time"

	"github.com/dogeorg/doge"
)

var ErrBlockNotFound = errors.New("block-not-found")

// Blockchain provides access to the Dogecoin Blockchain.
type Blockchain interface {

	// WaitForSync blocks until the Core Node is fully sync'd
	// Otherwise you can see very old blocks as "new" (e.g. when core is re-indexing)
	WaitForSync() Blockchain

	// RetryMode configures the number of retries, default 0 (Infinite)
	// Core returns a lot of spurious errors, so this should be more than 1.
	// When Core is starting up or syncing, most APIs fail (they return 500)
	// For a web client with client-side retries, use a low limit (2 or 3)
	RetryMode(attempts int, delay time.Duration) Blockchain

	// GetBlockHeader gets a `BlockHeader` (struct) from a block hash.
	// The 'Confirmations' field will be -1 if the block is off-chain (not on the main chain)
	// which is how DogeWalker detects chain reorganisation, i.e. a rollback.
	// 'blockHash' must be in reversed-hex notation (as displayed in block explorers)
	// Returns spec.BlockNotFound if the block doesn't exist.
	GetBlockHeader(blockHash string, ctx context.Context) (txn BlockHeader, err error)

	// GetBlock gets the raw block data from a block hash.
	// The 'dogeorg/doge' library can decode the block data.
	// 'blockHash' must be in reversed-hex notation (as displayed in block explorers)
	// Returns spec.BlockNotFound if the block is not present on the Core Node, or doesn't exist.
	GetBlock(blockHash string, ctx context.Context) (block doge.Block, err error)

	// GetBlockHash gets the hash of the block at block-height on the main chain (in reversed-hex notation)
	// Returns spec.BlockNotFound if blockHeight is above the tip of the chain.
	GetBlockHash(blockHeight int64, ctx context.Context) (hash string, err error)

	// GetBestBlockHash returns the hash of the block at the Tip of the main chain.
	GetBestBlockHash(ctx context.Context) (blockHash string, err error)

	// GetBlockCount returns the height of the block at the Tip of the main chain.
	GetBlockCount(ctx context.Context) (blockCount int64, err error)
}

// BlockHeader from Dogecoin Core
// Includes on-chain status (Confirmations = -1 means block is on a fork / orphan block)
// and current chain linkage (NextBlockHash)
// https://developer.bitcoin.org/reference/rpc/getblockheader.html
type BlockHeader struct {
	Hash              string  `json:"hash"`              // (string) the block hash (same as provided) (hex)
	Confirmations     int64   `json:"confirmations"`     // (numeric) The number of confirmations, or -1 if the block is not on the main chain
	Height            int64   `json:"height"`            // (numeric) The block height or index
	Version           uint32  `json:"version"`           // (numeric) The block version
	MerkleRoot        string  `json:"merkleroot"`        // (string) The merkle root (hex)
	Time              uint64  `json:"time"`              // (numeric) The block time in seconds since UNIX epoch (Jan 1 1970 GMT)
	MedianTime        uint64  `json:"mediantime"`        // (numeric) The median block time in seconds since UNIX epoch (Jan 1 1970 GMT)
	Nonce             uint32  `json:"nonce"`             // (numeric) The nonce
	Bits              string  `json:"bits"`              // (string) The bits (hex)
	Difficulty        float64 `json:"difficulty"`        // (numeric) The difficulty
	ChainWork         string  `json:"chainwork"`         // (string) Expected number of hashes required to produce the chain up to this block (hex)
	PreviousBlockHash string  `json:"previousblockhash"` // (string) The hash of the previous block (hex)
	NextBlockHash     string  `json:"nextblockhash"`     // (string) The hash of the next block (hex)
}
