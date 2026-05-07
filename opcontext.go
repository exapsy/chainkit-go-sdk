package chainkit

import "context"

type opContextKey struct{}

// OperationMetadata carries call-site context through the provider manager
// so that penalty records are enriched with actionable diagnostic data on failure.
//
// Attach it to a context with [WithOperationMetadata] before dispatching any
// blockchain operation. The manager reads it automatically inside
// recordFailureWithError; callers don't need to do anything else.
//
// Example (buythisfile-srv payment monitor):
//
//	ctx = chainkit.WithOperationMetadata(ctx, chainkit.OperationMetadata{
//	    Operation:  "GetBalance",
//	    Address:    address,
//	    Network:    cfg.BitcoinNetwork.String(),
//	    Touchpoint: "payment_monitor",
//	})
//	balance, err := providers.GetBalance(ctx, address)
type OperationMetadata struct {
	// Operation is the name of the blockchain operation being performed.
	// e.g. "GetBalance", "GetUTXOs", "PushTx", "GetTxStatus"
	Operation string

	// Address is the Bitcoin address being queried, if applicable.
	// Empty for non-address operations (e.g. PushTx, GetTxFees).
	Address string

	// Network is the Bitcoin network string.
	// e.g. "mainnet", "testnet3", "testnet4"
	Network string

	// Touchpoint identifies the service or component that initiated the call.
	// e.g. "payment_monitor", "consolidation", "cold_storage",
	//      "settlement", "admin_cli", "grpc_wallet"
	Touchpoint string

	// Extra holds arbitrary additional key/value pairs for operation-specific context.
	// e.g. map[string]string{"tx_type": "broadcast", "session_id": "abc123"}
	Extra map[string]string
}

// WithOperationMetadata attaches metadata to ctx. Call this at each blockchain
// call site before dispatching through the provider manager.
// It is safe to call with a nil Extra map.
func WithOperationMetadata(ctx context.Context, meta OperationMetadata) context.Context {
	return context.WithValue(ctx, opContextKey{}, meta)
}

// extractOperationMetadata retrieves metadata from ctx.
// Returns the zero-value OperationMetadata and false when not present.
func extractOperationMetadata(ctx context.Context) (OperationMetadata, bool) {
	meta, ok := ctx.Value(opContextKey{}).(OperationMetadata)
	return meta, ok
}
