package publish

import (
	"crypto/rand"
	"encoding/base64"
)

func newEditToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mftk_" + base64.RawURLEncoding.EncodeToString(b), nil
}
