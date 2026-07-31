package integrations

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

type testProvider struct {
	metadata ProviderMetadata
}

func (p testProvider) Metadata() ProviderMetadata { return p.metadata }

func TestProviderRegistryValidatesAndSortsProviders(t *testing.T) {
	registry := NewProviderRegistry()
	if err := registry.Register(nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Register(nil) error = %v, want ErrInvalidConfiguration", err)
	}
	if err := registry.Register(testProvider{}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Register(blank kind) error = %v, want ErrInvalidConfiguration", err)
	}

	for _, kind := range []string{"zeta", "alpha"} {
		provider := testProvider{metadata: ProviderMetadata{Kind: kind, DisplayName: kind}}
		if err := registry.Register(provider); err != nil {
			t.Fatalf("Register(%q): %v", kind, err)
		}
	}
	if err := registry.Register(testProvider{metadata: ProviderMetadata{Kind: "alpha"}}); !errors.Is(err, ErrProviderAlreadyExists) {
		t.Fatalf("duplicate Register error = %v, want ErrProviderAlreadyExists", err)
	}
	if _, err := registry.Get(" alpha "); err != nil {
		t.Fatalf("Get trims provider kind: %v", err)
	}
	if _, err := registry.Get("missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("Get missing error = %v, want ErrProviderNotFound", err)
	}

	providers := registry.List()
	if len(providers) != 2 || providers[0].Kind != "alpha" || providers[1].Kind != "zeta" {
		t.Fatalf("List = %#v, want alpha then zeta", providers)
	}
}

func TestProviderMetadataSupportsDeclaredCapabilities(t *testing.T) {
	metadata := ProviderMetadata{Capabilities: []Capability{CapabilityRosterRead, CapabilityIncrementalSync}}
	if !metadata.Supports(CapabilityRosterRead) {
		t.Fatal("Supports rejected declared capability")
	}
	if metadata.Supports(CapabilityAttendanceWrite) {
		t.Fatal("Supports accepted undeclared capability")
	}
}

func TestCredentialCipherRoundTripAndTamperDetection(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
	cipher, err := NewAESGCMCredentialCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher: %v", err)
	}

	plaintext := []byte(`{"access_token":"secret"}`)
	first, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("second Encrypt: %v", err)
	}
	if first.Version != CredentialEncryptionVersion || len(first.Nonce) == 0 {
		t.Fatalf("encrypted metadata = %#v", first)
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) || bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("repeated encryption reused ciphertext or nonce")
	}
	decrypted, err := cipher.Decrypt(first)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypt = %q, want %q", decrypted, plaintext)
	}

	tampered := first
	tampered.Ciphertext = append([]byte(nil), first.Ciphertext...)
	tampered.Ciphertext[0] ^= 0xff
	if _, err := cipher.Decrypt(tampered); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered Decrypt error = %v, want ErrAuthentication", err)
	}
	wrongVersion := first
	wrongVersion.Version++
	if _, err := cipher.Decrypt(wrongVersion); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("version Decrypt error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestCredentialCipherRejectsMissingAndInvalidKeys(t *testing.T) {
	for _, key := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("too short"))} {
		if _, err := NewAESGCMCredentialCipher(key); err == nil {
			t.Fatalf("NewAESGCMCredentialCipher(%q) succeeded", key)
		}
	}
	var cipher *AESGCMCredentialCipher
	if _, err := cipher.Encrypt([]byte("secret")); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("nil Encrypt error = %v, want ErrEncryptionUnavailable", err)
	}
}
