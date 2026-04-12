package config

import "strings"

// ChainInfo describes a blockchain network shortcut.
type ChainInfo struct {
	Name       string
	Header     string
	DefaultRPC string
}

// chains maps CLI shortcut names to their chain metadata.
var chains = map[string]ChainInfo{
	"eth":       {Name: "ethereum", Header: "ethereum", DefaultRPC: "https://eth.llamarpc.com"},
	"ethereum":  {Name: "ethereum", Header: "ethereum", DefaultRPC: "https://eth.llamarpc.com"},
	"base":      {Name: "base", Header: "base", DefaultRPC: "https://mainnet.base.org"},
	"solana":    {Name: "solana", Header: "solana", DefaultRPC: "https://api.mainnet-beta.solana.com"},
	"sol":       {Name: "solana", Header: "solana", DefaultRPC: "https://api.mainnet-beta.solana.com"},
	"matic":     {Name: "polygon", Header: "polygon", DefaultRPC: "https://polygon-rpc.com"},
	"polygon":   {Name: "polygon", Header: "polygon", DefaultRPC: "https://polygon-rpc.com"},
	"arb":       {Name: "arbitrum", Header: "arbitrum", DefaultRPC: "https://arb1.arbitrum.io/rpc"},
	"arbitrum":  {Name: "arbitrum", Header: "arbitrum", DefaultRPC: "https://arb1.arbitrum.io/rpc"},
	"op":        {Name: "optimism", Header: "optimism", DefaultRPC: "https://mainnet.optimism.io"},
	"optimism":  {Name: "optimism", Header: "optimism", DefaultRPC: "https://mainnet.optimism.io"},
	"avax":      {Name: "avalanche", Header: "avalanche", DefaultRPC: "https://api.avax.network/ext/bc/C/rpc"},
	"avalanche": {Name: "avalanche", Header: "avalanche", DefaultRPC: "https://api.avax.network/ext/bc/C/rpc"},
	"bsc":       {Name: "bsc", Header: "bsc", DefaultRPC: "https://bsc-dataseed.binance.org"},
}

// LookupChain resolves a chain shortcut name (case-insensitive).
func LookupChain(name string) (ChainInfo, bool) {
	info, ok := chains[strings.ToLower(strings.TrimSpace(name))]
	return info, ok
}

// ChainNames returns the canonical (short) chain shortcut names.
func ChainNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, info := range chains {
		if !seen[info.Name] {
			seen[info.Name] = true
			names = append(names, info.Name)
		}
	}
	return names
}
