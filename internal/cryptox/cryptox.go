// Package cryptox handles encryption of tokens at rest and of the
// export/import bundle used to copy state between machines.
package cryptox

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/secretbox"
)

const keySize = 32

// MasterKey loads the local at-rest encryption key from dataDir, generating
// one on first use. It never leaves the machine on its own; the export
// bundle is re-encrypted with a passphrase-derived key instead (see Seal).
func MasterKey(dataDir string) (*[keySize]byte, error) {
	path := filepath.Join(dataDir, "master.key")

	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != keySize {
			return nil, fmt.Errorf("master key at %s has unexpected length %d", path, len(data))
		}
		var key [keySize]byte
		copy(key[:], data)
		return &key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var key [keySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key[:], 0o600); err != nil {
		return nil, err
	}
	return &key, nil
}

// Seal encrypts plaintext with key using nacl/secretbox, returning
// nonce||ciphertext.
func Seal(key *[keySize]byte, plaintext []byte) ([]byte, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return secretbox.Seal(nonce[:], plaintext, &nonce, key), nil
}

// Open decrypts a nonce||ciphertext blob produced by Seal.
func Open(key *[keySize]byte, sealed []byte) ([]byte, error) {
	if len(sealed) < 24 {
		return nil, errors.New("ciphertext too short")
	}
	var nonce [24]byte
	copy(nonce[:], sealed[:24])

	plaintext, ok := secretbox.Open(nil, sealed[24:], &nonce, key)
	if !ok {
		return nil, errors.New("decryption failed: wrong key or corrupted data")
	}
	return plaintext, nil
}

// NewSalt returns a fresh random salt for passphrase key derivation.
func NewSalt() ([]byte, error) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	return salt, err
}

// DeriveKey derives a symmetric key from a user-supplied passphrase and salt
// using argon2id, for use with Seal/Open when encrypting export bundles that
// must be portable across machines (unlike MasterKey).
func DeriveKey(passphrase string, salt []byte) *[keySize]byte {
	derived := argon2.IDKey([]byte(passphrase), salt, 1, 64*1024, 4, keySize)
	var key [keySize]byte
	copy(key[:], derived)
	return &key
}
