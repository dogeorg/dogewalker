package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/spec"
)

// Core RPC error codes
const (
	BlockNotFound         = -5
	BlockHeightOutOfRange = -8
)

// NewCoreRPCClient returns a Dogecoin Core Node client.
// Thread-safe, can be shared across Goroutines.
func NewCoreRPCClient(rpcHost string, rpcPort int, rpcUser string, rpcPass string) spec.Blockchain {
	url := fmt.Sprintf("http://%s:%d", rpcHost, rpcPort)
	return &CoreRPCClient{url: url, user: rpcUser, pass: rpcPass, retryDelay: 5 * time.Second}
}

type CoreRPCClient struct {
	url        string
	user       string
	pass       string
	id         atomic.Uint64 // next unique request id
	attempts   int
	retryDelay time.Duration
	lock       sync.Mutex
}

func (c *CoreRPCClient) WaitForSync() spec.Blockchain {
	return c
}

func (c *CoreRPCClient) RetryMode(attempts int, delay time.Duration) spec.Blockchain {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.attempts = attempts
	c.retryDelay = delay
	return c
}

func (c *CoreRPCClient) GetBlockHeader(blockHash string, ctx context.Context) (txn spec.BlockHeader, err error) {
	attempts := c.attempts
	for attempts > 0 || c.attempts == 0 {
		decode := true // to get back JSON rather than HEX
		var code int
		code, err = c.Request(ctx, "getblockheader", []any{blockHash, decode}, &txn)
		if err != nil {
			if code == BlockNotFound {
				return spec.BlockHeader{}, spec.ErrBlockNotFound
			}
			if ctx.Err() != nil {
				return spec.BlockHeader{}, spec.ErrShutdown
			}
			log.Printf(`[CoreRPC] GetBlockHeader: %v (will retry)`, err)
			SleepWithContext(ctx, c.retryDelay)
			attempts -= 1
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBlock(blockHash string, ctx context.Context) (block doge.Block, err error) {
	attempts := c.attempts
	for attempts > 0 || c.attempts == 0 {
		decode := false // to get back HEX
		var hex string
		var code int
		var bytes []byte
		code, err = c.Request(ctx, "getblock", []any{blockHash, decode}, &hex)
		if err == nil {
			// if hex is invalid, most likely a Core failure
			bytes, err = doge.HexDecode(hex)
		}
		if err != nil {
			if code == BlockNotFound {
				return doge.Block{}, spec.ErrBlockNotFound
			}
			if ctx.Err() != nil {
				return doge.Block{}, spec.ErrShutdown
			}
			log.Printf(`[CoreRPC] GetBlock: %v (will retry)`, err)
			SleepWithContext(ctx, c.retryDelay)
			attempts -= 1
			continue
		}
		var valid bool
		block, valid = doge.DecodeBlock(bytes, true)
		if !valid {
			return doge.Block{}, fmt.Errorf("[CoreRPC] GetBlock: INVALID BLOCK! cannot parse block '%s'", blockHash)
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBlockHash(blockHeight int64, ctx context.Context) (hash string, err error) {
	attempts := c.attempts
	for attempts > 0 || c.attempts == 0 {
		var code int
		code, err = c.Request(ctx, "getblockhash", []any{blockHeight}, &hash)
		if err != nil {
			if code == BlockHeightOutOfRange {
				return "", spec.ErrBlockNotFound
			}
			if ctx.Err() != nil {
				return "", spec.ErrShutdown
			}
			log.Printf(`[CoreRPC] GetBlockHash: %v (will retry)`, err)
			SleepWithContext(ctx, c.retryDelay)
			attempts -= 1
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBestBlockHash(ctx context.Context) (blockHash string, err error) {
	attempts := c.attempts
	for attempts > 0 || c.attempts == 0 {
		_, err = c.Request(ctx, "getbestblockhash", []any{}, &blockHash)
		if err != nil {
			if ctx.Err() != nil {
				return "", spec.ErrShutdown
			}
			log.Printf(`[CoreRPC] GetBestBlockHash: %v (will retry)`, err)
			SleepWithContext(ctx, c.retryDelay)
			attempts -= 1
			continue
		}
		break // success
	}
	return
}

func (c *CoreRPCClient) GetBlockCount(ctx context.Context) (blockCount int64, err error) {
	attempts := c.attempts
	for attempts > 0 || c.attempts == 0 {
		_, err = c.Request(ctx, "getblockcount", []any{}, &blockCount)
		if err != nil {
			if ctx.Err() != nil {
				return 0, spec.ErrShutdown
			}
			log.Printf(`[CoreRPC] GetBlockCount: %v (will retry)`, err)
			SleepWithContext(ctx, c.retryDelay)
			attempts -= 1
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
	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewBuffer(payload))
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
