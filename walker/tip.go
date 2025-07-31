package walker

import (
	"context"
	"log"
	"time"

	"github.com/dogeorg/dogewalker/spec"
)

/*
 * Find a starting block near the tip of the chain.
 *
 * Use this if you don't need to walk the entire blockchain, but instead want to
 * start somewhere near the current tip of the chain.
 *
 * `ctx` allows cancellation/timeout, otherwise pass nil.
 * `client` must implement spec.Blockchain, e.g. `core.NewCoreRPCClient()`
 * `blocksBeforeTip` allows you to start e.g. 100 blocks before the current tip.
 *
 * Returns the hash and height of the tip, or a block before the tip.
 */
func FindTheTip(ctx context.Context, client spec.Blockchain, blocksBelowTip int64) (hash string, height int64) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		panic("FindTheTip: `client` is required; cannot be nil.")
	}
	height = fetchBlockCount(ctx, client)
	if blocksBelowTip > 0 {
		height -= blocksBelowTip
	}
	if height < 0 {
		height = 0
	}
	hash = fetchBlockHash(ctx, client, height)
	return
}

func fetchBlockHash(ctx context.Context, client spec.Blockchain, height int64) string {
	for {
		hash, err := client.GetBlockHash(height, ctx)
		if err != nil {
			log.Println("FindTheTip: error retrieving block hash (will retry):", err)
			sleepWithCancel(ctx, RETRY_DELAY)
		} else {
			return hash
		}
	}
}

func fetchBlockCount(ctx context.Context, client spec.Blockchain) int64 {
	for {
		count, err := client.GetBlockCount(ctx)
		if err != nil {
			log.Println("FindTheTip: error retrieving block count (will retry):", err)
			sleepWithCancel(ctx, RETRY_DELAY)
		} else {
			return count
		}
	}
}

func sleepWithCancel(ctx context.Context, duration time.Duration) (cancelled bool) {
	select {
	case <-ctx.Done(): // receive context cancel
		return true
	case <-time.After(duration):
		return false
	}
}
