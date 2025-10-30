package spec

import (
	"context"
	"errors"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/doge/koinu"
)

var ErrBlockNotFound = errors.New("block-not-found")
var ErrTxNotFound = errors.New("transaction-not-found")
var ErrShutdown = errors.New("shutting-down")

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

	// GetBlockchainInfo returns information about the block chain.
	GetBlockchainInfo(ctx context.Context) (info BlockchainInfo, err error)

	// EstimateFee returns the estimated fee per kilobyte for a transaction to be included in a block.
	EstimateFee(ctx context.Context, confirmTarget int) (feePerKB koinu.Koinu, err error)

	// GetRawMempool returns the raw mempool contents from the Core Node.
	GetRawMempool(ctx context.Context) (mempool RawMempool, err error)

	// GetRawMempoolTxList returns the raw mempool transaction list from the Core Node.
	GetRawMempoolTxList(ctx context.Context) (txlist []string, err error)

	// GetRawTransaction returns the raw transaction data from the Core Node.
	GetRawTransaction(ctx context.Context, txID string) (tx doge.BlockTx, err error)

	// SendRawTransaction sends a raw transaction to the Core Node.
	SendRawTransaction(ctx context.Context, txHex string, maxFeeRate koinu.Koinu) (txid string, err error)
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

// BlockchainInfo from Core
type BlockchainInfo struct {
	Chain                string  `json:"chain"`                // (string) current network name (main, test, regtest)
	Blocks               int64   `json:"blocks"`               // (numeric) the height of the most-work fully-validated chain. The genesis block has height 0
	Headers              int64   `json:"headers"`              // (numeric) the current number of headers we have validated
	BestBlockHash        string  `json:"bestblockhash"`        // (string) the hash of the currently best block
	Difficulty           float64 `json:"difficulty"`           // (numeric) the current difficulty
	MedianTime           int64   `json:"mediantime"`           // (numeric) median time for the current best block
	VerificationProgress float64 `json:"verificationprogress"` // (numeric) estimate of verification progress [0..1]
	InitialBlockDownload bool    `json:"initialblockdownload"` // (boolean) (debug information) estimate of whether this node is in Initial Block Download mode
	ChainWord            string  `json:"chainwork"`            // (string) total amount of work in active chain, in hexadecimal
	SizeOnDisk           int64   `json:"size_on_disk"`         // (numeric) the estimated size of the block and undo files on disk
	Pruned               bool    `json:"pruned"`               // (boolean) if the blocks are subject to pruning
	PruneHeight          int64   `json:"pruneheight"`          // (numeric) lowest-height complete block stored (only present if pruning is enabled)
	AutomaticPruning     bool    `json:"automatic_pruning"`    // (boolean) whether automatic pruning is enabled (only present if pruning is enabled)
	PruneTargetSize      int64   `json:"prune_target_size"`    // (numeric) the target size used by pruning (only present if automatic pruning is enabled)
}

// RawMempool is the response from the `getrawmempool` Core API.
// https://developer.bitcoin.org/reference/rpc/getrawmempool.html
type RawMempool = map[string]RawMempoolTx

// RawMempoolTx is the response from the `getrawmempool` Core API.
// https://developer.bitcoin.org/reference/rpc/getrawmempool.html
type RawMempoolTx struct {
	Time            int64       `json:"time"`
	Height          int64       `json:"height"`
	Fee             koinu.Koinu `json:"fee"`
	DescendantCount int         `json:"descendantcount"`
	DescendantSize  int64       `json:"descendantsize"`
	DescendantFees  int64       `json:"descendantfees"`
	AncestorCount   int         `json:"ancestorcount"`
	AncestorSize    int64       `json:"ancestorsize"`
	AncestorFees    int64       `json:"ancestorfees"`
	Depends         []string    `json:"depends"`
	Replaceable     bool        `json:"bip125-replaceable"`
	Unbroadcast     bool        `json:"unbroadcast"`
}

// MempoolEntry is the response from the `getmempoolentry` Core API.
type MempoolEntry struct {
	Fee             koinu.Koinu `json:"fee"`
	ModifiedFee     koinu.Koinu `json:"modifiedfee"`
	Time            time.Time   `json:"time"`
	Height          int64       `json:"height"`
	DescendantCount int         `json:"descendantcount"`
	DescendantSize  int64       `json:"descendantsize"`
	DescendantFees  int64       `json:"descendantfees"`
	Depends         []string    `json:"depends"`
	Replaceable     bool        `json:"bip125-replaceable"`
	Unbroadcast     bool        `json:"unbroadcast"`
}

// RawTransaction is the response from the `getrawtransaction` Core API.
// https://developer.bitcoin.org/reference/rpc/getrawtransaction.html
type RawTransaction struct {
	InActiveChain bool                 `json:"in_active_chain"` // Whether specified block is in the active chain or not
	Hex           string               `json:"hex"`             // The serialized transaction data (hex-encoded)
	TxID          string               `json:"txid"`            // The transaction id (hex-encoded, reversed)
	Hash          string               `json:"hash"`            // The transaction hash (differs from txid for witness transactions)
	Size          int64                `json:"size"`            // The serialized transaction size
	Vsize         int64                `json:"vsize"`           // The virtual transaction size (differs from size for witness transactions)
	Weight        int64                `json:"weight"`          // The transaction's weight (between vsize*4-3 and vsize*4)
	Version       int                  `json:"version"`         // The transaction-encoding version
	Locktime      int64                `json:"locktime"`        // The lock time
	Vin           []RawTransactionVin  `json:"vin"`             // The transaction inputs
	Vout          []RawTransactionVout `json:"vout"`            // The transaction outputs
	Blockhash     string               `json:"blockhash"`       // The block hash (hex-encoded)
	Confirmations int64                `json:"confirmations"`   // The number of confirmations
	Blocktime     int64                `json:"blocktime"`       // The block time expressed in UNIX epoch time
	Time          int64                `json:"time"`            // The same as blocktime
}

// RawTransactionVin is the response from the `getrawtransaction` Core API.
// https://developer.bitcoin.org/reference/rpc/getrawtransaction.html
type RawTransactionVin struct {
	TxID      string                  `json:"txid"`        // The transaction id (hex-encoded)
	Vout      int                     `json:"vout"`        // The output number (uint32)
	ScriptSig RawTransactionScriptSig `json:"scriptSig"`   // The script
	Sequence  int64                   `json:"sequence"`    // The script sequence number
	Witness   []string                `json:"txinwitness"` // The witness data (if any, hex-encoded)
}

type RawTransactionScriptSig struct {
	Asm string `json:"asm"` // The script disassembly
	Hex string `json:"hex"` // The script bytes (hex-encoded)
}

// RawTransactionVout is the response from the `getrawtransaction` Core API.
// https://developer.bitcoin.org/reference/rpc/getrawtransaction.html
type RawTransactionVout struct {
	Index        int                        `json:"n"`            // The output index (uint32)
	Value        koinu.Koinu                `json:"value"`        // The value in DOGE (int64)
	ScriptPubKey RawTransactionScriptPubKey `json:"scriptPubKey"` // The script
}

type RawTransactionScriptPubKey struct {
	Asm       string   `json:"asm"`       // The script disassembly
	Hex       string   `json:"hex"`       // The script bytes (hex-encoded)
	ReqSigs   int      `json:"reqSigs"`   // The number of required signatures
	Type      string   `json:"type"`      // The script type, eg 'pubkeyhash'
	Addresses []string `json:"addresses"` // Doge Addresses found in the script
}
