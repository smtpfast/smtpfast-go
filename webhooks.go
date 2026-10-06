package smtpfast

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifyWebhookSignature checks that a webhook request came from SMTPfast.
// signature is the X-SMTPfast-Signature header: the hex HMAC-SHA256 of the
// raw request body, keyed with the webhook's signing secret. Pass the body
// exactly as received, before any JSON parsing.
func VerifyWebhookSignature(payload []byte, signature, secret string) bool {
	if signature == "" || secret == "" {
		return false
	}
	got, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hmac.Equal(mac.Sum(nil), got)
}
