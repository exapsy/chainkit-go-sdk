# Error Message Improvements

## Overview

This document describes improvements made to error messages across blockchain provider implementations to provide clearer diagnostics and better debugging capabilities.

## Philosophy

Error messages should contain **meaningful, non-redundant information**. The context (which provider failed) is already known by the caller - the error message's "real estate" should be used for:

1. **HTTP status codes** - Essential for understanding API failures
2. **Response bodies** - Actual error details from the API
3. **Operation parameters** - Which address, txID, etc. caused the failure
4. **Action descriptions** - What operation failed (not who performed it)

### What NOT to Include

❌ **Provider name prefixes** - The caller already knows which provider was used:

```go
// Bad - wastes message space
return nil, fmt.Errorf("BlockCypher GetBalance(%s) failed: %w", address, err)
```

✅ **Focus on the actual error**:

```go
// Good - concise and informative
return nil, fmt.Errorf("fetch balance for %s: %w", address, err)
```

## Problem Statement

Previously, many provider operations returned vague error messages like "operation failed" without including critical context such as:

- HTTP status codes (404, 429, 500, etc.)
- Response bodies from APIs
- Input parameters that caused the failure

This made debugging difficult, as the scoring engine's penalty history showed entries like:

```
04:15:48 PM  error  operation failed  −3.0
04:11:18 PM  error  operation failed  −3.0
```

Without knowing whether the error was a rate limit (429), not found (404), authentication failure (401), or server error (500), it was impossible to diagnose the root cause.

## Solution

### 1. HTTP Status Codes with Response Bodies

All API errors now include status codes and response bodies:

**Before:**

```go
if resp.StatusCode != http.StatusOK {
    return nil, fmt.Errorf("API request failed")
}
```

**After:**

```go
if resp.StatusCode != http.StatusOK {
    body, _ := io.ReadAll(resp.Body)
    return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}
```

### 2. Contextual Parameters

Error messages include relevant parameters without redundant prefixes:

**Before:**

```go
return nil, fmt.Errorf("failed to fetch tx status: %w", err)
```

**After:**

```go
return nil, fmt.Errorf("fetch tx status %s: %w", txID, err)
```

### 3. Action-Focused Descriptions

Errors describe **what failed**, not **who** tried to do it:

**Before:**

```go
return nil, fmt.Errorf("BlockCypher failed to broadcast transaction: %w", err)
```

**After:**

```go
return nil, fmt.Errorf("broadcast transaction: %w", err)
```

## Changes by Provider

### Blockcypher

- `GetUTXOs`: `fetch address %s: %w`
- `ValidateAddress`: `validate address %s: %w`
- `GetBalance`: `fetch balance for %s: %w`
- `PushTx`: `broadcast transaction: %w`
- `GetTxStatus`: `fetch tx status %s: %w`

### Blockstream

- `GetUTXOs`: `fetch UTXOs for %s: %w`
- `GetTxStatus`: `fetch tx status %s: %w`
- `callAPI`: Returns `HTTP %d: %s` with response body
- Rate limits: `HTTP 429: rate limit exceeded`
- Not found: `HTTP 404: not found`
- Auth failures: `HTTP 401: unauthorized after token refresh`

### BlockchainCom

- `GetBalance`: `HTTP %d: %s` for API errors
- `PushTx`: `HTTP %d: %s` for API errors
- Generic errors: `create request`, `read response body`, `parse balance`

### Mempool

- All API errors: `HTTP %d: %s` format
- Uses structured `types.RequestError` with consistent formatting

### Coingecko

- All API errors: `HTTP %d: %s` format
- Consistent error wrapping

### Binance

- All API errors: `HTTP %d: %s` format

### Bitrefcom

- All API errors: `HTTP %d: %s` via `callAPI` helper
- Generic errors: `marshal request body`, `parse response`

### Tatum

- RPC errors: `RPC error %d: %s` with error message
- HTTP errors: `HTTP %d: %s` with response body
- Generic errors: `marshal RPC request`, `parse tx status response`

## Critical Fix: Scoring Engine

**The most important change** was in `scoring/engine.go`. Previously, the scoring engine was wrapping ALL error messages with an "operation failed" prefix:

**Before:**

```go
case EventOperationFailed:
    reason := "operation failed"
    if event.Error != nil {
        reason = fmt.Sprintf("operation failed: %s", event.Error.Error())
    }
```

This meant even when providers returned clean, descriptive errors like `HTTP 404: not found`, they were being displayed as `operation failed: HTTP 404: not found`.

**After:**

```go
case EventOperationFailed:
    reason := "operation failed"
    if event.Error != nil {
        reason = event.Error.Error()  // ✅ Use the error directly
    }
```

Now the provider's error message is used as-is, without redundant wrapping.

**Impact**: Without this fix, all the provider improvements would be pointless - the scoring engine was hiding them behind "operation failed" prefixes.

## Impact on Diagnostics

The scoring engine now records much more useful penalty history:

**Before:**

```
04:15:48 PM  error         operation failed           −3.0
04:15:48 PM  rate limit    rate limited (HTTP 429)    −20.0
04:15:48 PM  error         operation failed           −3.0
```

**After:**

```
04:15:48 PM  error         fetch balance for tb1q...: HTTP 404   −3.0
04:15:48 PM  rate limit    HTTP 429: rate limit exceeded         −20.0
04:15:48 PM  error         fetch tx status abc123...: not found  −3.0
```

## Best Practices for Future Error Messages

When adding new provider operations or modifying existing ones, follow these guidelines:

### 1. NO Provider Name Prefixes

```go
// ❌ Bad - redundant, caller knows the provider
fmt.Errorf("ProviderName OperationName failed: %w", err)

// ✅ Good - concise, informative
fmt.Errorf("fetch balance for %s: %w", address, err)
```

### 2. Always Include HTTP Status and Response Body

```go
// ✅ Good - essential debugging info
if resp.StatusCode != http.StatusOK {
    body, _ := io.ReadAll(resp.Body)
    return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

// ❌ Bad - no HTTP details
if resp.StatusCode != http.StatusOK {
    return nil, fmt.Errorf("API request failed")
}
```

### 3. Include Relevant Parameters

```go
// ✅ Good - helps identify which specific resource failed
fmt.Errorf("fetch balance for %s: %w", address, err)
fmt.Errorf("fetch tx status %s: %w", txID, err)

// ❌ Bad - no context about what was being fetched
fmt.Errorf("operation failed: %w", err)
```

### 4. Use Specific Messages for Known HTTP Status Codes

```go
// ✅ Good - explicit about the error type
switch resp.StatusCode {
case http.StatusNotFound:
    resp.Body.Close()
    return nil, fmt.Errorf("HTTP 404: not found")
case http.StatusTooManyRequests:
    resp.Body.Close()
    return nil, fmt.Errorf("HTTP 429: rate limit exceeded")
case http.StatusUnauthorized:
    resp.Body.Close()
    return nil, fmt.Errorf("HTTP 401: unauthorized")
default:
    body, _ := io.ReadAll(resp.Body)
    resp.Body.Close()
    return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

// ❌ Bad - loses HTTP status information
if resp.StatusCode != http.StatusOK {
    return nil, errors.New("request failed")
}
```

### 5. Use Action Verbs, Not "Failed to..."

```go
// ✅ Good - concise action descriptions
fmt.Errorf("create request: %w", err)
fmt.Errorf("parse response: %w", err)
fmt.Errorf("read response body: %w", err)

// ❌ Bad - verbose and redundant
fmt.Errorf("failed to create request: %w", err)
fmt.Errorf("failed to parse response: %w", err)
```

### 6. Wrap Errors to Preserve Context

```go
// ✅ Good - preserves original error while adding context
return nil, fmt.Errorf("fetch tx status %s: %w", txID, err)

// ❌ Bad - loses original error information
return nil, errors.New("operation failed")
```

## Error Classification

The scoring engine uses `ClassifyOperationEvent` in `scoring/events.go` to categorize errors:

### Rate Limit Detection

Errors containing these strings are classified as `EventRateLimited` (−20.0 penalty):

- "rate limit"
- "too many requests"
- "429"
- "quota exceeded"
- "throttled"

### Generic Failures

All other errors are classified as `EventOperationFailed` (−3.0 penalty).

### Implementation Note

The classification happens at the manager level (`manager.go`), which already knows:

- Which provider was called
- Which operation was attempted
- The response time

Therefore, error messages should focus on **what went wrong**, not **who** or **where**.

## Testing

After making error message changes:

1. **Build test:**

   ```bash
   go build ./bitcoin/providers/...
   ```

2. **Run tests:**

   ```bash
   go test ./bitcoin/providers/... -v
   ```

3. **Verify diagnostics:**
   - Check the admin UI penalty history
   - Ensure error messages are descriptive and actionable
   - Verify HTTP status codes are visible
   - Confirm no redundant provider name prefixes

## Example: Good vs Bad

### Bad Example (Before)

```go
func (p *blockcypher) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
    addr, err := p.Client.GetAddr(address, nil)
    if err != nil {
        // ❌ Redundant "BlockCypher", no parameter context
        return chainkit.Balance{}, fmt.Errorf("BlockCypher failed to fetch address details: %w", err)
    }
    // ...
}
```

**Diagnostic Output:**

```
04:11:18 PM  error  BlockCypher failed to fetch address details: some error  −3.0
```

### Good Example (After)

```go
func (p *blockcypher) GetBalance(ctx context.Context, address string) (chainkit.Balance, error) {
    addr, err := p.Client.GetAddr(address, nil)
    if err != nil {
        // ✅ Concise, includes address, no redundancy
        return chainkit.Balance{}, fmt.Errorf("fetch balance for %s: %w", address, err)
    }
    // ...
}
```

**Diagnostic Output:**

```
04:11:18 PM  error  fetch balance for tb1q...xyz: HTTP 404  −3.0
```

The second version immediately tells you:

- **What failed**: fetching a balance
- **Which address**: `tb1q...xyz`
- **Why it failed**: HTTP 404

The provider name is already visible in the UI context, so it doesn't need to be in the message.

## Related Files

- `manager.go`: Provider manager that records failures and knows provider context
- `scoring/events.go`: Event classification logic (knows provider from ScoreEvent)
- `scoring/engine.go`: Scoring engine that records penalty history with provider info
- `bitcoin/providers/*.go`: Provider implementations

## Version History

- **2024-01**: Initial error message improvements
  - **CRITICAL**: Fixed scoring engine to stop wrapping errors with "operation failed" prefix
  - Removed redundant provider name prefixes from all providers
  - Standardized HTTP status code format (`HTTP %d: %s`)
  - Added contextual parameters (addresses, txIDs)
  - Focused on action descriptions, not "failed to..." patterns
  - Documented philosophy: error messages should contain non-redundant information
