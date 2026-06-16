package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/waystones/oapif-go/internal/auth"
)

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyHMAC_Valid(t *testing.T) {
	sig := sign("supersecret", "eyJjb2xsZWN0aW9ucyI6W119")
	if !auth.VerifyHMAC([]byte("supersecret"), "eyJjb2xsZWN0aW9ucyI6W119", sig) {
		t.Error("expected valid HMAC to return true")
	}
}

func TestVerifyHMAC_WrongKey(t *testing.T) {
	sig := sign("correctkey", "payload")
	if auth.VerifyHMAC([]byte("wrongkey"), "payload", sig) {
		t.Error("expected wrong key to return false")
	}
}

func TestVerifyHMAC_TamperedPayload(t *testing.T) {
	sig := sign("secret", "original")
	if auth.VerifyHMAC([]byte("secret"), "tampered", sig) {
		t.Error("expected tampered payload to return false")
	}
}

func TestVerifyHMAC_EmptySig(t *testing.T) {
	if auth.VerifyHMAC([]byte("secret"), "payload", "") {
		t.Error("expected empty sig to return false")
	}
}

func TestVerifyHMAC_InvalidHexSig(t *testing.T) {
	if auth.VerifyHMAC([]byte("secret"), "payload", "not-hex-garbage!!") {
		t.Error("expected invalid hex sig to return false")
	}
}

func TestVerifyHMAC_EmptySecret(t *testing.T) {
	// With empty secret the HMAC is deterministic; verify it still works correctly.
	sig := sign("", "payload")
	if !auth.VerifyHMAC([]byte(""), "payload", sig) {
		t.Error("expected empty secret with matching sig to return true")
	}
}
