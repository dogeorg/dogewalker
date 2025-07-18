package walker

import (
	"fmt"

	"github.com/dogeorg/doge"
)

// chainFromName returns ChainParams for: 'main', 'test', 'regtest'.
func ChainFromName(chainName string) (*doge.ChainParams, error) {
	switch chainName {
	case "main":
		return &doge.DogeMainNetChain, nil
	case "test":
		return &doge.DogeTestNetChain, nil
	case "regtest":
		return &doge.DogeRegTestChain, nil
	default:
		return &doge.ChainParams{}, fmt.Errorf("unknown chain: %v", chainName)
	}
}
