package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ============================================================
// Webhook signature verification
// ============================================================
//
// chainkit-cloud signs every outbound webhook with HMAC-SHA256 over
// the literal request body. The signature lands in the
// `X-Chainkit-Signature` header in this shape:
//
//	t=<unix-seconds>,v1=<hex-mac>
//
// where v1=hex(HMAC-SHA256(secret, "<unix-seconds>." + body)).
//
// The timestamp is INSIDE the signed input AND in the header — the
// header value can be parsed, the signed input reconstructed, and
// the MAC re-computed. Embedding the timestamp lets the receiver
// reject replays that fall outside a chosen freshness window.
//
// The "v1=" prefix is a versioning hook. If we ever rotate the
// scheme (different hash, key derivation, AEAD), receivers that
// understand v1 can still verify the legacy traffic.
//
// VERIFICATION IS ON THE RAW BYTES YOU RECEIVED. Do NOT re-marshal
// a parsed JSON object — Go's encoding/json may reorder fields or
// rewrite whitespace, producing bytes that don't match what was
// signed.
//
// Usage:
//
//	body, _ := io.ReadAll(r.Body)
//	if err := payment.VerifyWebhook(body, r.Header.Get("X-Chainkit-Signature"), secret); err != nil {
//	    // 400 — reject without acting on the body
//	    return
//	}
//	// parse body, dispatch on event_type, etc.

// Sentinel errors. Production callers should sentinel-match; the
// error messages describe what to investigate (header missing, the
// scheme is unknown, the MAC didn't match, the timestamp is stale).
var (
	// ErrWebhookSignatureMissing means the header is empty.
	ErrWebhookSignatureMissing = errors.New("payment: webhook signature header is empty")
	// ErrWebhookSignatureMalformed means the header was non-empty
	// but doesn't parse as `t=...,v1=...` (or similar).
	ErrWebhookSignatureMalformed = errors.New("payment: webhook signature header is malformed")
	// ErrWebhookSignatureUnsupportedScheme means the header had no
	// recognised algorithm version (e.g. v1=...).
	ErrWebhookSignatureUnsupportedScheme = errors.New("payment: webhook signature scheme not recognised")
	// ErrWebhookSignatureMismatch means the MAC didn't match — most
	// likely cause is a wrong secret or a body that was modified in
	// transit. Constant-time compared.
	ErrWebhookSignatureMismatch = errors.New("payment: webhook signature does not match")
	// ErrWebhookSignatureExpired means the timestamp fell outside
	// the allowed tolerance window. Most often a replayed delivery
	// from hours ago.
	ErrWebhookSignatureExpired = errors.New("payment: webhook signature timestamp outside tolerance window")
)

// VerifyWebhookOptions configures freshness and (in the future)
// per-scheme verification knobs.
type VerifyWebhookOptions struct {
	// Tolerance is the maximum absolute difference between the
	// signature timestamp and `Now()`. Defaults to 5 minutes when
	// zero. Receivers behind a slow / clock-skewed network may want
	// to increase this; high-security receivers may want to drop it.
	Tolerance time.Duration

	// Now overrides the wall clock for tests. Production callers
	// leave this nil — VerifyWebhook uses time.Now().
	Now func() time.Time
}

// defaultTolerance matches Stripe's published "5 minutes" cutoff.
const defaultTolerance = 5 * time.Minute

// VerifyWebhook checks the X-Chainkit-Signature header against the
// supplied body + secret. Returns nil on success; one of the
// sentinel errors above on failure.
//
// The `opts` variadic accepts zero or one VerifyWebhookOptions; only
// the first is honoured. The variadic shape exists so the common
// 3-arg call (body, header, secret) reads as a one-liner.
func VerifyWebhook(body []byte, header, secret string, opts ...VerifyWebhookOptions) error {
	o := VerifyWebhookOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Tolerance == 0 {
		o.Tolerance = defaultTolerance
	}
	if o.Now == nil {
		o.Now = time.Now
	}

	if strings.TrimSpace(header) == "" {
		return ErrWebhookSignatureMissing
	}
	parts, err := parseSignatureHeader(header)
	if err != nil {
		return err
	}

	tsStr, ok := parts["t"]
	if !ok {
		return fmt.Errorf("%w: missing t=", ErrWebhookSignatureMalformed)
	}
	tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: t= is not an integer", ErrWebhookSignatureMalformed)
	}

	sigHex, ok := parts["v1"]
	if !ok {
		return ErrWebhookSignatureUnsupportedScheme
	}
	expectedSig, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("%w: v1 is not hex", ErrWebhookSignatureMalformed)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	// "<timestamp>.<body>" — must match chainkit-cloud's signing
	// input byte-for-byte. Writes to an in-memory hasher never fail,
	// so the error/count are intentionally discarded.
	_, _ = fmt.Fprintf(mac, "%d.", tsUnix)
	mac.Write(body)
	computed := mac.Sum(nil)
	if !hmac.Equal(expectedSig, computed) {
		return ErrWebhookSignatureMismatch
	}

	// Freshness check happens AFTER the MAC succeeds. An attacker
	// who can replay a stale-but-valid delivery has a stale-valid
	// MAC; rejecting on tolerance defends against that.
	now := o.Now()
	delta := now.Unix() - tsUnix
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > o.Tolerance {
		return ErrWebhookSignatureExpired
	}
	return nil
}

// parseSignatureHeader splits "k1=v1,k2=v2" into a map. Keys may
// recur in the spec (e.g. "v1=a,v1=b" for dual-signed transitions)
// — for v1 we only use the latest, matching Stripe's behaviour.
func parseSignatureHeader(header string) (map[string]string, error) {
	out := make(map[string]string, 2)
	for _, raw := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("%w: pair %q has no =", ErrWebhookSignatureMalformed, raw)
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if k == "" {
			return nil, fmt.Errorf("%w: empty key in %q", ErrWebhookSignatureMalformed, raw)
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, ErrWebhookSignatureMalformed
	}
	return out, nil
}
