package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// sampleSecret is a fixed test vector — matches what
// chainkit-cloud-srv generates with the "whsec_" + base64url
// pattern. Real secrets are 32 bytes of randomness.
const sampleSecret = "whsec_test_aaaabbbbccccddddeeeeffff"

// signFixture builds a valid X-Chainkit-Signature header for the
// given body + timestamp, matching chainkit-cloud-srv's signing
// implementation (services/payments/webhook_delivery.go#signPayload).
// Used both by the tests' "happy path" and to construct deliberately-
// broken variants.
func signFixture(body []byte, ts time.Time, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts.Unix())
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func TestVerifyWebhook_HappyPath(t *testing.T) {
	body := []byte(`{"event_type":"invoice.confirmed","public_id":"abc"}`)
	now := time.Unix(1_750_000_000, 0)
	sig := signFixture(body, now, sampleSecret)

	err := VerifyWebhook(body, sig, sampleSecret, VerifyWebhookOptions{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("happy-path verify failed: %v", err)
	}
}

func TestVerifyWebhook_ToleranceWindow(t *testing.T) {
	body := []byte(`{"ok":true}`)
	signedAt := time.Unix(1_750_000_000, 0)
	sig := signFixture(body, signedAt, sampleSecret)

	// 4 minutes elapsed — within default 5-minute tolerance.
	withinWindow := func() time.Time { return signedAt.Add(4 * time.Minute) }
	if err := VerifyWebhook(body, sig, sampleSecret, VerifyWebhookOptions{Now: withinWindow}); err != nil {
		t.Errorf("expected pass within 5-min window, got %v", err)
	}

	// 6 minutes elapsed — outside default 5-minute tolerance.
	outsideWindow := func() time.Time { return signedAt.Add(6 * time.Minute) }
	if err := VerifyWebhook(body, sig, sampleSecret, VerifyWebhookOptions{Now: outsideWindow}); !errors.Is(err, ErrWebhookSignatureExpired) {
		t.Errorf("expected ErrWebhookSignatureExpired at 6 min, got %v", err)
	}

	// Custom larger tolerance — 6 min becomes acceptable again.
	if err := VerifyWebhook(body, sig, sampleSecret, VerifyWebhookOptions{
		Now:       outsideWindow,
		Tolerance: 10 * time.Minute,
	}); err != nil {
		t.Errorf("expected pass within 10-min override, got %v", err)
	}

	// Future timestamps also rejected (clock skew or attacker-set).
	pastNow := func() time.Time { return signedAt.Add(-10 * time.Minute) }
	if err := VerifyWebhook(body, sig, sampleSecret, VerifyWebhookOptions{Now: pastNow}); !errors.Is(err, ErrWebhookSignatureExpired) {
		t.Errorf("expected ErrWebhookSignatureExpired for future ts, got %v", err)
	}
}

func TestVerifyWebhook_TamperingDetected(t *testing.T) {
	body := []byte(`{"event_type":"invoice.confirmed","public_id":"abc"}`)
	now := time.Unix(1_750_000_000, 0)
	sig := signFixture(body, now, sampleSecret)
	at := VerifyWebhookOptions{Now: func() time.Time { return now }}

	// Modified body — MAC mismatch.
	tamperedBody := []byte(`{"event_type":"invoice.confirmed","public_id":"abc","amount_stolen":1}`)
	if err := VerifyWebhook(tamperedBody, sig, sampleSecret, at); !errors.Is(err, ErrWebhookSignatureMismatch) {
		t.Errorf("expected mismatch on body tamper, got %v", err)
	}

	// Wrong secret — MAC mismatch.
	if err := VerifyWebhook(body, sig, "wrong_secret", at); !errors.Is(err, ErrWebhookSignatureMismatch) {
		t.Errorf("expected mismatch on wrong secret, got %v", err)
	}

	// Timestamp altered post-hoc — header re-parses but the MAC
	// was computed against the original ts so it won't match.
	parts := strings.Split(sig, ",")
	parts[0] = "t=" + fmt.Sprintf("%d", now.Add(1*time.Second).Unix())
	tamperedSig := strings.Join(parts, ",")
	if err := VerifyWebhook(body, tamperedSig, sampleSecret, at); !errors.Is(err, ErrWebhookSignatureMismatch) {
		t.Errorf("expected mismatch on timestamp tamper, got %v", err)
	}
}

func TestVerifyWebhook_HeaderErrors(t *testing.T) {
	body := []byte(`{}`)
	at := VerifyWebhookOptions{Now: time.Now}

	cases := []struct {
		name   string
		header string
		want   error
	}{
		{"empty", "", ErrWebhookSignatureMissing},
		{"only-spaces", "   ", ErrWebhookSignatureMissing},
		{"no-equals", "tnoequals,v1=abc", ErrWebhookSignatureMalformed},
		{"no-t", "v1=abc", ErrWebhookSignatureMalformed},
		{"non-numeric-t", "t=not-a-number,v1=abc", ErrWebhookSignatureMalformed},
		{"only-t-no-v1", "t=1750000000", ErrWebhookSignatureUnsupportedScheme},
		{"unknown-scheme", "t=1750000000,v9=abc", ErrWebhookSignatureUnsupportedScheme},
		{"non-hex-v1", "t=1750000000,v1=not-hex-zzzz", ErrWebhookSignatureMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyWebhook(body, tc.header, sampleSecret, at)
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyWebhook_DefaultsWhenOptsOmitted(t *testing.T) {
	// Three-argument call (no opts) uses time.Now() + 5 min tolerance.
	// We sign with NOW so the default-Now branch passes.
	body := []byte(`{"ok":true}`)
	now := time.Now()
	sig := signFixture(body, now, sampleSecret)
	if err := VerifyWebhook(body, sig, sampleSecret); err != nil {
		t.Errorf("3-arg verify with current-time signature failed: %v", err)
	}
}
