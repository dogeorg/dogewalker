package walker

import (
	"context"
	"log"
	"time"

	"github.com/dogeorg/dogewalker/v2/core"
	"github.com/dogeorg/dogewalker/v2/spec"
)

const TIP_RETRY_COUNT = 0               // keep trying until the context is cancelled.
const TIP_RETRY_DELAY = 5 * time.Second // for RPC retries.

/*
 * Find a starting block near the tip of the chain.
 *
 * Use this if you don't need to walk the entire blockchain, but instead want to
 * start somewhere near the current tip of the chain.
 *
 * `ctx` allows cancellation and ContextWithCoreRPCRetry(), otherwise pass nil.
 * `client` must implement spec.Blockchain, e.g. `core.NewCoreRPCClient()`
 * `blocksBeforeTip` allows you to start e.g. 100 blocks before the current tip.
 *
 * Returns the hash and height of the tip, or a block before the tip.
 */
func FindTheTip(ctx context.Context, client spec.Blockchain, blocksBelowTip int64) (hash string, height int64) {
	if client == nil {
		panic("FindTheTip: `client` is required; cannot be nil.")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(core.CoreRPCRetryKey{}) == nil {
		// By default, retry until the context is cancelled.
		ctx = core.ContextWithCoreRPCRetry(ctx, TIP_RETRY_COUNT, TIP_RETRY_DELAY, "FindTheTip")
	}
	var err error
	height, err = client.GetBlockCount(ctx)
	if err != nil {
		log.Println("FindTheTip: cannot retrieve block count: %w", err)
		return "", 0 // cancelled or too many attempts
	}
	if blocksBelowTip > 0 {
		height -= blocksBelowTip
	}
	if height < 0 {
		height = 0
	}
	hash, err = client.GetBlockHash(ctx, height)
	if err != nil {
		log.Println("FindTheTip: cannot retrieve block hash: %w", err)
		return "", 0 // cancelled or too many attempts
	}
	return
}
