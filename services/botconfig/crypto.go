package botconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// MinSecretLen is the shortest accepted BOTCONFIG_SECRET.
	MinSecretLen = 32
	hkdfInfo     = "eyn-botconfig-v1"
	keyVersion1  = byte(1)
	nonceLen     = 12
)

var errDecrypt = errors.New("botconfig: cannot decrypt secret")

// deriveKey is HKDF-SHA256(secret, info="eyn-botconfig-v1") → 32 bytes.
func deriveKey(secret string) ([]byte, error) {
	if len(secret) < MinSecretLen {
		return nil, errors.New("botconfig: BOTCONFIG_SECRET must be at least 32 characters")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(secret), nil, []byte(hkdfInfo)), key); err != nil {
		return nil, err
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// seal returns version(1) | nonce(12) | ciphertext+tag, bound to aad.
func seal(key []byte, plaintext, aad string) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{keyVersion1}, nonce...)
	return g.Seal(out, nonce, []byte(plaintext), []byte(aad)), nil
}

// open fails closed on a wrong key, wrong aad (swapped rows), unknown version or tampering.
func open(key, blob []byte, aad string) (string, error) {
	if len(blob) < 1+nonceLen+16 || blob[0] != keyVersion1 {
		return "", errDecrypt
	}
	g, err := newGCM(key)
	if err != nil {
		return "", errDecrypt
	}
	pt, err := g.Open(nil, blob[1:1+nonceLen], blob[1+nonceLen:], []byte(aad))
	if err != nil {
		return "", errDecrypt
	}
	return string(pt), nil
}
