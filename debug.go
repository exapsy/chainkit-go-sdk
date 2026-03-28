package chainkit

import (
	"context"
	"time"
)

// DebugInfo represents blockchain provider debug information
type DebugInfo struct {
	BlockchainProvider string
	ProcessedAt        time.Time
	ProviderChain      []string
}

// ExtractDebugInfo extracts blockchain provider information from context
func ExtractDebugInfo(ctx context.Context) *DebugInfo {
	providerName, ok := GetProviderName(ctx)
	if !ok {
		providerName = "unknown"
	}

	return &DebugInfo{
		BlockchainProvider: providerName,
		ProcessedAt:        time.Now(),
		ProviderChain:      []string{providerName},
	}
}
