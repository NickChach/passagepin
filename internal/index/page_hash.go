package index

import (
	"crypto/sha256"
	"encoding/hex"
)

// pageTextHash is the saved-index contract used by both builds and checks:
// SHA-256 of the exact extracted UTF-8 bytes, including blank/tokenless text.
func pageTextHash(text string) string {
	digest := sha256.Sum256([]byte(text))
	return hex.EncodeToString(digest[:])
}
