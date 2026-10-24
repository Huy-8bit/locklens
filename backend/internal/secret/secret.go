// Package secret encrypts target connection strings at rest.
//
// The master key comes from the environment (LOCKLENS_MASTER_KEY) and is never
// written to the monitoring store. Ciphertexts use AES-256-GCM with a random
// 96-bit nonce and a version byte, so the format can evolve.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const formatV1 = 1

// ErrDecrypt is returned for any ciphertext that cannot be opened. It does not
// say why, so it cannot leak information about the key or the plaintext.
var ErrDecrypt = errors.New("cannot decrypt stored secret (wrong LOCKLENS_MASTER_KEY or corrupted data)")

// Box encrypts and decrypts with one master key.
type Box struct {
	aead cipher.AEAD
}

// ParseKey decodes a base64 (standard or URL alphabet, padded or not) 32-byte
// key. Generate one with: openssl rand -base64 32
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("master key is empty")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("master key must decode to 32 bytes, got %d", len(b))
			}
			return b, nil
		}
	}
	return nil, errors.New("master key is not valid base64")
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext. additional binds the ciphertext to a context (for
// example "target-dsn") so it cannot be swapped into another column.
func (b *Box) Seal(plaintext, additional []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, formatV1)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plaintext, additional), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(ciphertext, additional []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < 1+ns+b.aead.Overhead() || ciphertext[0] != formatV1 {
		return nil, ErrDecrypt
	}
	plain, err := b.aead.Open(nil, ciphertext[1:1+ns], ciphertext[1+ns:], additional)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
