# DogeWalker

DogeWalker is a Go library that walks the Dogecoin blockchain from a
nominated starting block, fetching and decoding each block and allowing
you to process it.

It also notifies you when a "fork" is encountered on-chain, meaning one
or more blocks need to be backed-out and their effect undone.

Forks can be handled effectively by recording the block-height in your
system with each change caused by a block. When a fork occurs, back out
all changes tagged with a greater block-height than the `LastValidHeight`
given.

Fork information also includes all the block ids on the fork, and if
requested, all transactions in those blocks.
