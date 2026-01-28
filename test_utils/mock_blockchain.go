package test_utils

import (
	"context"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/doge/koinu"
	"github.com/dogeorg/dogewalker/spec"
)

// MockChain is a simple in-memory implementation of spec.Blockchain for tests.
type MockChain struct {
	headers map[string]spec.BlockHeader
	blocks  map[string]doge.Block
	heights map[int64]string
}

func NewMockChain() *MockChain {
	return &MockChain{
		headers: make(map[string]spec.BlockHeader),
		blocks:  make(map[string]doge.Block),
		heights: make(map[int64]string),
	}
}

func (m *MockChain) WaitForSync(ctx context.Context) bool                        { return false }
func (m *MockChain) RetryMode(attempts int, delay time.Duration) spec.Blockchain { return m }

func (m *MockChain) GetBlockHeader(blockHash string, ctx context.Context) (spec.BlockHeader, error) {
	h, ok := m.headers[blockHash]
	if !ok {
		return spec.BlockHeader{}, spec.ErrBlockNotFound
	}
	return h, nil
}

func (m *MockChain) GetBlock(blockHash string, ctx context.Context) (doge.Block, error) {
	b, ok := m.blocks[blockHash]
	if !ok {
		return doge.Block{}, spec.ErrBlockNotFound
	}
	return b, nil
}

func (m *MockChain) GetBlockHash(blockHeight int64, ctx context.Context) (string, error) {
	h, ok := m.heights[blockHeight]
	if !ok {
		return "", spec.ErrBlockNotFound
	}
	return h, nil
}

// Unused by current tests; minimal stubs provided.
func (m *MockChain) GetBestBlockHash(ctx context.Context) (string, error) { return "", nil }
func (m *MockChain) GetBlockCount(ctx context.Context) (int64, error) {
	return int64(len(m.heights)) - 1, nil
}
func (m *MockChain) GetBlockchainInfo(ctx context.Context) (spec.BlockchainInfo, error) {
	return spec.BlockchainInfo{}, nil
}
func (m *MockChain) EstimateFee(ctx context.Context, confirmTarget int) (koinuPerKB koinu.Koinu, err error) {
	return 0, nil
}
func (m *MockChain) GetRawMempool(ctx context.Context) (spec.RawMempool, error) {
	return spec.RawMempool{}, nil
}
func (m *MockChain) GetRawMempoolTxList(ctx context.Context) ([]string, error) {
	return []string{}, nil
}
func (m *MockChain) GetRawTransaction(ctx context.Context, txID string) (doge.BlockTx, error) {
	return doge.BlockTx{}, nil
}
func (m *MockChain) SendRawTransaction(ctx context.Context, txHex string) (string, error) {
	return "", nil
}

// Helpers for constructing chains in tests.
func (m *MockChain) AddHeader(h spec.BlockHeader) {
	m.headers[h.Hash] = h
}

func (m *MockChain) AddBlock(hash string) {
	m.blocks[hash] = doge.Block{}
}

func (m *MockChain) SetHeight(height int64, hash string) {
	m.heights[height] = hash
}
