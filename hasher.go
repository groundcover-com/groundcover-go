package groundcover

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// IdentityHasher pseudonymizes identity fields (user.id / user.email) at the SDK
// boundary. Implementations should use a keyed function (e.g. HMAC), not a plain
// hash, so values cannot be trivially reversed via a dictionary.
type IdentityHasher interface {
	// HashIdentity returns the pseudonymized form of value. An empty input must
	// map to an empty output.
	HashIdentity(value string) string
}

// HMACHasher is a keyed HMAC-SHA256 IdentityHasher. The zero value hashes with
// an empty key; prefer NewHMACHasher.
type HMACHasher struct {
	key []byte
}

// NewHMACHasher returns an HMACHasher keyed with the given secret.
func NewHMACHasher(key []byte) *HMACHasher {
	cp := make([]byte, len(key))
	copy(cp, key)
	return &HMACHasher{key: cp}
}

// HashIdentity returns the hex-encoded HMAC-SHA256 of value, or "" for "".
func (h *HMACHasher) HashIdentity(value string) string {
	if value == "" {
		return ""
	}
	mac := hmac.New(sha256.New, h.key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
