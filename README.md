# DogeWalker

DogeWalker is a Go library that walks the Dogecoin blockchain from a
specified starting block, fetching and decoding each block and allowing
you to process it.

It also notifies you when a "reorganisation" is encountered on-chain,
meaning one or more blocks need to be backed-out and their effect undone.

Reorgs can be handled effectively by recording the block-height in your
database with each change caused by a block. When a reorg occurs, back out
all changes tagged with a greater block-height than the `LastValidHeight`
given in the `undo` message.

Reorg information also includes all the block ids being undone, and if
requested in `WalkerOptions`, all transactions in those blocks.
