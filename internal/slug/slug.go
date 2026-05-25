package slug

import (
	"crypto/rand"
	"math/big"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const slugLen = 8

// Generate returns a cryptographically random 8-char base62 slug.
func Generate() (string, error) {
	b := make([]byte, slugLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

// IsReserved reports whether s matches a reserved word exactly.
func IsReserved(s string) bool {
	_, ok := reservedWords[s]
	return ok
}
