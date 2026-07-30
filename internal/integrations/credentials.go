package integrations

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const CredentialEncryptionVersion = 1

type EncryptedCredentials struct {
	Ciphertext []byte
	Nonce      []byte
	Version    int
}

type CredentialCipher interface {
	Encrypt([]byte) (EncryptedCredentials, error)
	Decrypt(EncryptedCredentials) ([]byte, error)
}

type AESGCMCredentialCipher struct {
	aead cipher.AEAD
}

// NewAESGCMCredentialCipher decodes the environment-provided AES-256 key and
// constructs the only boundary allowed to encrypt or decrypt provider secrets.
func NewAESGCMCredentialCipher(encodedKey string) (*AESGCMCredentialCipher, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		return nil, ErrEncryptionUnavailable
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encodedKey)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%w: INTEGRATION_CREDENTIAL_KEY must be a base64-encoded 32-byte key", ErrInvalidConfiguration)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential encryption mode: %w", err)
	}
	return &AESGCMCredentialCipher{aead: aead}, nil
}

func (c *AESGCMCredentialCipher) Encrypt(plaintext []byte) (EncryptedCredentials, error) {
	if c == nil || c.aead == nil {
		return EncryptedCredentials{}, ErrEncryptionUnavailable
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return EncryptedCredentials{}, fmt.Errorf("generate credential nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, nil)
	return EncryptedCredentials{Ciphertext: ciphertext, Nonce: nonce, Version: CredentialEncryptionVersion}, nil
}

func (c *AESGCMCredentialCipher) Decrypt(encrypted EncryptedCredentials) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrEncryptionUnavailable
	}
	if encrypted.Version != CredentialEncryptionVersion {
		return nil, fmt.Errorf("%w: unsupported credential encryption version %d", ErrInvalidConfiguration, encrypted.Version)
	}
	plaintext, err := c.aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt integration credentials: %w", ErrAuthentication)
	}
	return plaintext, nil
}
