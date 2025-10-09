package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
)

func getRefreshSecret() []byte {
	sec := os.Getenv("REFRESH_TOKEN_SECRET")
	return []byte(sec)
}

func GenerateRefreshToken() (plain string, digestHex string, err error) {
	secret := getRefreshSecret()
	if len(secret) == 0 {
		return "", "", errors.New("REFRESH_TOKEN_SECRET not set")
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	digestHex = DigestRefreshToken(plain)
	return
}

func DigestRefreshToken(plain string) string {
	h := hmac.New(sha256.New, getRefreshSecret())
	h.Write([]byte(plain))
	return hex.EncodeToString(h.Sum(nil))
}
