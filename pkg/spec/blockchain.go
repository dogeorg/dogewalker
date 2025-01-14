package spec

// Blockchain provides access to the Dogecoin Blockchain.
type Blockchain interface {
	GetBlockHeader(blockHash string) (txn BlockHeader, err error)
	GetBlock(blockHash string) (hex string, err error)
	GetBlockHash(blockHeight int64) (hash string, err error)
	GetBestBlockHash() (blockHash string, err error)
	GetBlockCount() (blockCount int64, err error)
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
