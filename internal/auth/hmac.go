package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifyHMAC returns true when sig is the correct HMAC-SHA256 hex digest of
// payload keyed with secret. Uses hmac.Equal for constant-time comparison.
// Returns false immediately when sig is empty.
func VerifyHMAC(secret []byte, payload, sig string) bool {
	if sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload)) //nolint:errcheck — hmac.Hash.Write never returns an error
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}
