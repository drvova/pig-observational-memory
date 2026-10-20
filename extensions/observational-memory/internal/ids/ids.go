// Package ids derives the deterministic memory record ids.
package ids

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashID is the first 12 hex characters of the SHA-256 of content.
func HashID(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:12]
}
