package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/doge/koinu"
	"github.com/dogeorg/dogewalker/spec"
)

const WAIT_FOR_SYNC_RETRY_DELAY = 30 * time.Second

// Core RPC error codes
const (
	InvalidAddressOrKey   = -5  // RPC_INVALID_ADDRESS_OR_KEY (invalid address or key)
	InvalidParameter      = -8  // RPC_INVALID_PARAMETER (invalid, missing or duplicate parameter)
	VerifyError           = -25 // RPC_VERIFY_ERROR (general error during transaction or block submission)
	VerifyRejected        = -26 // RPC_VERIFY_REJECTED (transaction or block was rejected by network rules)
	AlreadyInUTXOSet      = -27 // RPC_VERIFY_ALREADY_IN_UTXO_SET (transaction already in utxo set)
	InWarmup              = -28 // RPC_IN_WARMUP (client still warming up)
	BlockNotFound         = -5  // deprecated (use InvalidAddressOrKey instead)
	BlockHeightOutOfRange = -8  // deprecated (use InvalidParameter instead)
)

// NewCoreRPCClient returns a Dogecoin Core Node client.
// Thread-safe, can be shared across Goroutines.
// If rpcHostOrUrl is a URL, it will be used directly.
// If rpcHostOrUrl is a hostname, it will be used with the port.
func NewCoreRPCClient(rpcHostOrUrl string, rpcPort int, rpcUser string, rpcPass string) spec.Blockchain {
	parsedUrl, err := url.Parse(rpcHostOrUrl)
	var url string
	if err != nil || parsedUrl.Scheme == "" {
		url = fmt.Sprintf("http://%s:%d", rpcHostOrUrl, rpcPort)
	} else {
		url = rpcHostOrUrl
	}

	return &CoreRPCClient{URL: url, user: rpcUser, pass: rpcPass, retryDelay: 5 * time.Second}
}

type CoreRPCClient struct {
	URL            string
	user           string
	pass           string
	id             atomic.Uint64 // next unique request id
	attemptsConfig int
	retryDelay     time.Duration
	lock           sync.Mutex
}

func (c *CoreRPCClient) WaitForSync(ctx context.Context) bool {
	for {
		info, err := c.GetBlockchainInfo(ctx)
		if err != nil {
			if err == spec.ErrShutdown {
				return true // stopping
			}
			log.Printf(`[CoreRPC] WaitForSync: %v (will retry)\n`, err)
			SleepWithContext(ctx, WAIT_FOR_SYNC_RETRY_DELAY)
			continue
		}
		if info.InitialBlockDownload {
			log.Printf(`[CoreRPC] WaitForSync: initial block download in progress, waiting...\n`)
			SleepWithContext(ctx, WAIT_FOR_SYNC_RETRY_DELAY)
			continue
		}
		threshold := info.Headers - 10
		if info.Blocks < threshold {
			log.Printf(`[CoreRPC] WaitForSync: blocks are behind headers, waiting...\n`)
			SleepWithContext(ctx, WAIT_FOR_SYNC_RETRY_DELAY)
			continue
		}
		return false // done, core node is synced
	}
}

func (c *CoreRPCClient) RetryMode(attempts int, delay time.Duration) spec.Blockchain {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.attemptsConfig = attempts
	c.retryDelay = delay
	return c
}

func (c *CoreRPCClient) GetBlockHeader(blockHash string, ctx context.Context) (txn spec.BlockHeader, err error) {
	attempts := c.attemptsConfig
	for {
		var code int
		decode := true // to get back JSON rather than HEX
		code, err = c.Request(ctx, "getblockheader", []any{blockHash, decode}, &txn)
		if err != nil {
			if ctx.Err() != nil {
				return spec.BlockHeader{}, spec.ErrShutdown
			}
			if code == InvalidAddressOrKey {
				return spec.BlockHeader{}, spec.ErrBlockNotFound
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return spec.BlockHeader{}, fmt.Errorf("[CoreRPC] GetBlockHeader: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetBlockHeader: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBlock(blockHash string, ctx context.Context) (block doge.Block, err error) {
	attempts := c.attemptsConfig
	for {
		block, err = c.requestBlock(ctx, blockHash)
		if err != nil {
			if ctx.Err() != nil {
				return doge.Block{}, spec.ErrShutdown
			}
			if err == spec.ErrBlockNotFound {
				return doge.Block{}, spec.ErrBlockNotFound
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return doge.Block{}, fmt.Errorf("[CoreRPC] GetBlock: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetBlock: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) requestBlock(ctx context.Context, blockHash string) (doge.Block, error) {
	var hex string
	code, err := c.Request(ctx, "getblock", []any{blockHash, false}, &hex)
	if err != nil {
		if code == InvalidAddressOrKey {
			return doge.Block{}, spec.ErrBlockNotFound
		}
		return doge.Block{}, fmt.Errorf("getblock: %w", err)
	}
	bytes, err := doge.HexDecode(hex)
	if err != nil {
		return doge.Block{}, fmt.Errorf("hex decode: %w", err)
	}
	var valid bool
	block, valid := doge.DecodeBlock(bytes, true)
	if !valid {
		return doge.Block{}, fmt.Errorf("INVALID BLOCK! cannot parse block '%s'", blockHash)
	}
	return block, nil
}

func (c *CoreRPCClient) GetBlockHash(blockHeight int64, ctx context.Context) (hash string, err error) {
	attempts := c.attemptsConfig
	for {
		var code int
		code, err = c.Request(ctx, "getblockhash", []any{blockHeight}, &hash)
		if err != nil {
			if code == InvalidParameter { // block height out of range
				return "", spec.ErrBlockNotFound
			}
			if ctx.Err() != nil {
				return "", spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return "", fmt.Errorf("[CoreRPC] GetBlockHash: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetBlockHash: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBestBlockHash(ctx context.Context) (blockHash string, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "getbestblockhash", []any{}, &blockHash)
		if err != nil {
			if ctx.Err() != nil {
				return "", spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return "", fmt.Errorf("[CoreRPC] GetBestBlockHash: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetBestBlockHash: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBlockCount(ctx context.Context) (blockCount int64, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "getblockcount", []any{}, &blockCount)
		if err != nil {
			if ctx.Err() != nil {
				return 0, spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return 0, fmt.Errorf("[CoreRPC] GetBlockCount: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetBlockCount: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBlockchainInfo(ctx context.Context) (info spec.BlockchainInfo, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "getblockchaininfo", []any{}, &info)
		if err != nil {
			if ctx.Err() != nil {
				return spec.BlockchainInfo{}, spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return spec.BlockchainInfo{}, fmt.Errorf("[CoreRPC] GetBlockchainInfo: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetBlockchainInfo: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) EstimateFee(ctx context.Context, confirmTarget int) (feePerKB koinu.Koinu, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "estimatefee", []any{confirmTarget}, &feePerKB)
		if err != nil {
			if ctx.Err() != nil {
				return 0, spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return 0, fmt.Errorf("[CoreRPC] EstimateFee: %w", err)
				}
			}
			log.Printf(`[CoreRPC] EstimateFee: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	if feePerKB < 0 {
		return 0, errors.New("[CoreRPC] EstimateFee: fee-rate is negative")
	}
	return
}

func (c *CoreRPCClient) GetRawMempool(ctx context.Context) (mem spec.RawMempool, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "getrawmempool", []any{true}, &mem)
		if err != nil {
			if ctx.Err() != nil {
				return spec.RawMempool{}, spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return spec.RawMempool{}, fmt.Errorf("[CoreRPC] GetRawMempool: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetRawMempool: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetRawMempoolTxList(ctx context.Context) (txlist []string, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "getrawmempool", []any{false}, &txlist)
		if err != nil {
			if ctx.Err() != nil {
				return []string{}, spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return []string{}, fmt.Errorf("[CoreRPC] GetRawMempoolTxList: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetRawMempoolTxList: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetRawTransaction(ctx context.Context, txID string) (tx doge.BlockTx, err error) {
	attempts := c.attemptsConfig
	for {
		tx, err = c.requestRawTransaction(ctx, txID)
		if err != nil {
			if ctx.Err() != nil {
				return doge.BlockTx{}, spec.ErrShutdown
			}
			if err == spec.ErrTxNotFound {
				return doge.BlockTx{}, spec.ErrTxNotFound
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return doge.BlockTx{}, fmt.Errorf("[CoreRPC] GetRawTransaction: %w", err)
				}
			}
			log.Printf(`[CoreRPC] GetRawTransaction: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) requestRawTransaction(ctx context.Context, txID string) (doge.BlockTx, error) {
	var hex string
	code, err := c.Request(ctx, "getrawtransaction", []any{txID, false}, &hex)
	if err != nil {
		if code == InvalidAddressOrKey {
			return doge.BlockTx{}, spec.ErrTxNotFound
		}
		return doge.BlockTx{}, fmt.Errorf("getrawtransaction: %w", err)
	}
	bytes, err := doge.HexDecode(hex)
	if err != nil {
		return doge.BlockTx{}, fmt.Errorf("hex decode: %w", err)
	}
	var valid bool
	tx, valid := doge.DecodeTx(bytes, true)
	if !valid {
		return doge.BlockTx{}, fmt.Errorf("INVALID TRANSACTION! cannot parse '%s'", txID)
	}
	return tx, nil
}

func (c *CoreRPCClient) SendRawTransaction(ctx context.Context, txHex string) (txid string, err error) {
	attempts := c.attemptsConfig
	for {
		_, err = c.Request(ctx, "sendrawtransaction", []any{txHex}, &txid)
		if err != nil {
			if ctx.Err() != nil {
				return "", spec.ErrShutdown
			}
			if c.attemptsConfig != 0 { // infinite retries
				attempts -= 1
				if attempts <= 0 {
					return "", fmt.Errorf("[CoreRPC] SendRawTransaction: %w", err)
				}
			}
			log.Printf(`[CoreRPC] SendRawTransaction: %v (will retry)\n`, err)
			SleepWithContext(ctx, c.retryDelay)
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) Request(ctx context.Context, method string, params []any, result any) (int, error) {
	id := c.id.Add(1) // each request should use a unique ID (workers process requests)
	// Core only (seems to) process one request at a time, and returns spurious 500 errors
	// whenever it's busy processing a block, for example. Let's not push it.
	c.lock.Lock()
	defer c.lock.Unlock()
	body := rpcRequest{
		Method: method,
		Params: params,
		Id:     id,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("json-rpc marshal request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewBuffer(payload))
	if err != nil {
		return 0, fmt.Errorf("json-rpc request: %v", err)
	}
	req.SetBasicAuth(c.user, c.pass)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("json-rpc transport: %v", err)
	}
	// MUST read all of res.Body and call res.Close,
	// otherwise the underlying connection cannot be re-used.
	defer res.Body.Close()
	res_bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, fmt.Errorf("json-rpc read response: %v", err)
	}
	// cannot use json.NewDecoder: "The decoder introduces its own buffering
	// and may read data from r beyond the JSON values requested."
	var rpcres rpcResponse
	err = json.Unmarshal(res_bytes, &rpcres)
	if err != nil {
		// could not parse the response
		if res.StatusCode != 200 {
			return 0, fmt.Errorf("json-rpc status code: %s", res.Status)
		}
		return 0, fmt.Errorf("json-rpc unmarshal response: %v", err)
	}
	if rpcres.Id != body.Id {
		return 0, fmt.Errorf("json-rpc wrong ID returned: %v vs %v", rpcres.Id, body.Id)
	}
	if rpcres.Error.Code != 0 {
		return rpcres.Error.Code, rpcres.Error
	}
	if rpcres.Result == nil {
		return 0, fmt.Errorf("json-rpc missing result")
	}

	if result == nil {
		// We are not expecting or interested in the result for some requests, do not try to unmarshal into `nil`.
		return 0, nil
	}

	err = json.Unmarshal(*rpcres.Result, result)
	if err != nil {
		return 0, fmt.Errorf("json-rpc unmarshal result: %v | %v", err, string(*rpcres.Result))
	}
	return 0, nil
}

type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	Id     uint64 `json:"id"`
}

// Error: {"result":null,"error":{"code":-28,"message":"Loading block index..."},"id":"curltext"}
type rpcResponse struct {
	Id     uint64           `json:"id"`
	Result *json.RawMessage `json:"result"`
	Error  rpcError         `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e rpcError) Error() string {
	return fmt.Sprintf("json-rpc error: %v, %v", e.Code, e.Message)
}

func SleepWithContext(ctx context.Context, duration time.Duration) (cancelled bool) {
	select {
	case <-ctx.Done(): // receive context cancel
		return true
	case <-time.After(duration):
		return false
	}
}
