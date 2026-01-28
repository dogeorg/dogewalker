package walker

import (
	"context"
	"testing"

	"github.com/dogeorg/doge"
	"github.com/dogeorg/dogewalker/spec"
	"github.com/dogeorg/dogewalker/test_utils"
)

// Helpers

func recvMessage[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	default:
		t.Fatalf("expecting channel receive, got nothing")
		return *new(T)
	}
}

// Test: findLastProcessedBlock returns configured c.lastProcessed when it is specified.
func TestFindLastProcessedBlock_UsesExistingLastProcessed(t *testing.T) {
	mc := test_utils.NewMockChain()
	genesis := doge.DogeMainNetChain.GenesisBlock
	mc.SetHeight(0, genesis) // ensure chain check passes

	const resumeHash = "b123"
	c := dogeWalker{
		output:        make(chan BlockOrUndo, 1),
		client:        mc,
		chain:         &doge.DogeMainNetChain,
		lastProcessed: resumeHash,
	}
	c.Context = context.Background()

	last, ok := c.findLastProcessedBlock()
	if !ok {
		t.Fatalf("service unexpectedly stopped")
	}
	if last != resumeHash {
		t.Fatalf("expected lastProcessed %s, got %s", resumeHash, last)
	}
	// Ensure no message was emitted (since we didn't process genesis).
	select {
	case msg := <-c.output:
		t.Fatalf("unexpected message emitted: %#v", msg)
	default:
		// ok
	}
}

// Test: findLastProcessedBlock starts from genesis when no resume hash is provided.
func TestFindLastProcessedBlock_StartsFromGenesis(t *testing.T) {
	mc := test_utils.NewMockChain()
	genesis := doge.DogeMainNetChain.GenesisBlock
	mc.SetHeight(0, genesis)
	mc.AddHeader(spec.BlockHeader{
		Hash:          genesis,
		Confirmations: 1,
		Height:        0,
	})
	mc.AddBlock(genesis)

	c := dogeWalker{
		output:        make(chan BlockOrUndo, 4),
		client:        mc,
		chain:         &doge.DogeMainNetChain,
		lastProcessed: "",
	}
	c.Context = context.Background()

	last, ok := c.findLastProcessedBlock()
	if !ok {
		t.Fatalf("service unexpectedly stopped")
	}
	if last != genesis {
		t.Fatalf("expected lastProcessed %s, got %s", genesis, last)
	}
	msg := recvMessage(t, c.output)
	if msg.Block == nil {
		t.Fatalf("expected Block message for genesis, got %#v", msg)
	}
	if msg.Block.Hash != genesis || msg.Height != 0 {
		t.Fatalf("unexpected block message: %#v", msg)
	}
}

// Test: checkForNewBlocks follows a linear chain forward and emits Idle at tip.
func TestCheckForNewBlocks_FollowsForwardAndIdles(t *testing.T) {
	mc := test_utils.NewMockChain()
	// Build chain b0 -> b1 -> b2
	b0, b1, b2 := "b0", "b1", "b2"
	mc.AddHeader(spec.BlockHeader{Hash: b0, Confirmations: 1, Height: 0, NextBlockHash: b1})
	mc.AddHeader(spec.BlockHeader{Hash: b1, Confirmations: 1, Height: 1, PreviousBlockHash: b0, NextBlockHash: b2})
	mc.AddHeader(spec.BlockHeader{Hash: b2, Confirmations: 1, Height: 2, PreviousBlockHash: b1})
	mc.AddBlock(b1)
	mc.AddBlock(b2)

	c := dogeWalker{
		output: make(chan BlockOrUndo, 8),
		client: mc,
		chain:  &doge.DogeMainNetChain,
	}
	c.Context = context.Background()

	newLast, ok := c.checkForNewBlocks(b0)
	if !ok {
		t.Fatalf("service unexpectedly stopped")
	}
	// Expect: Block(b1), Block(b2), Idle
	msg1 := recvMessage(t, c.output)
	if msg1.Block == nil || msg1.Block.Hash != b1 || msg1.Height != 1 {
		t.Fatalf("expected Block(b1), got %#v", msg1)
	}
	msg2 := recvMessage(t, c.output)
	if msg2.Block == nil || msg2.Block.Hash != b2 || msg2.Height != 2 {
		t.Fatalf("expected Block(b2), got %#v", msg2)
	}
	msg3 := recvMessage(t, c.output)
	if !msg3.Idle || msg3.LastProcessedBlock != b2 || msg3.Height != 2 {
		t.Fatalf("expected Idle msg, got %#v", msg3)
	}
	if newLast != b2 {
		t.Fatalf("expected last block to be b2, got %s", newLast)
	}
}

// Test: checkForNewBlocks emits Undo for a mid-chain fork (undo b3,b2), then follows b2p,b3p.
func TestCheckForNewBlocks_EmitsUndoThenFollowsNewFork(t *testing.T) {
	mc := test_utils.NewMockChain()
	// Build:
	// Original chain:     b0 -> b1 -> b2 -> b3
	// New fork:           b0 -> b1 -> b2p -> b3p
	// Start from lastProcessed=b3, expect undo to b1, then follow b2p, b3p
	b0, b1, b2, b3, b2p, b3p := "b0", "b1", "b2", "b3", "b2p", "b3p"
	// On-chain base
	mc.AddHeader(spec.BlockHeader{Hash: b0, Confirmations: 1, Height: 0, NextBlockHash: b1})
	mc.AddHeader(spec.BlockHeader{Hash: b1, Confirmations: 1, Height: 1, PreviousBlockHash: b0, NextBlockHash: b2p})
	// Off-chain branch (to be undone)
	mc.AddHeader(spec.BlockHeader{Hash: b2, Confirmations: -1, Height: 2, PreviousBlockHash: b1, NextBlockHash: b3})
	mc.AddHeader(spec.BlockHeader{Hash: b3, Confirmations: -1, Height: 3, PreviousBlockHash: b2})
	// On-chain replacement
	mc.AddHeader(spec.BlockHeader{Hash: b2p, Confirmations: 1, Height: 2, PreviousBlockHash: b1, NextBlockHash: b3p})
	mc.AddHeader(spec.BlockHeader{Hash: b3p, Confirmations: 1, Height: 3, PreviousBlockHash: b2p})
	mc.AddBlock(b2p)
	mc.AddBlock(b3p)

	c := dogeWalker{
		output: make(chan BlockOrUndo, 8),
		client: mc,
		chain:  &doge.DogeMainNetChain,
	}
	c.Context = context.Background()

	// Start from b3, expect undo to b1, then follow b2p, b3p, then idle at b3p.
	newLast, ok := c.checkForNewBlocks(b3)
	if !ok {
		t.Fatalf("service unexpectedly stopped")
	}

	// Expect: Undo(to b1, undo [b3,b2]), then Block(b2p), Block(b3p), then Idle
	msg1 := recvMessage(t, c.output)
	if msg1.Undo == nil {
		t.Fatalf("expected an Undo event, got %#v", msg1)
	}
	if msg1.Undo.LastValidHash != b1 || msg1.Height != 1 {
		t.Fatalf("expected undo to b1 (height 1), got %#v", msg1.Undo)
	}
	if len(msg1.Undo.UndoBlocks) != 2 || msg1.Undo.UndoBlocks[0] != b3 || msg1.Undo.UndoBlocks[1] != b2 {
		t.Fatalf("expected undo [b3 b2], got %#v", msg1.Undo.UndoBlocks)
	}

	msg2 := recvMessage(t, c.output)
	if msg2.Block == nil || msg2.Block.Hash != b2p || msg2.Height != 2 {
		t.Fatalf("expected Block b2p (h=2), got %#v", msg2)
	}
	msg3 := recvMessage(t, c.output)
	if msg3.Block == nil || msg3.Block.Hash != b3p || msg3.Height != 3 {
		t.Fatalf("expected Block b3p (h=3), got %#v", msg3)
	}
	msg4 := recvMessage(t, c.output)
	if !msg4.Idle || msg4.LastProcessedBlock != b3p || msg4.Height != 3 {
		t.Fatalf("expected Idle msg at b3p, got %#v", msg4)
	}
	if newLast != b3p {
		t.Fatalf("expected last block to be b3p, got %s", newLast)
	}
}
