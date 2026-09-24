package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const prefixV1 = "enc:v1:"

var (
	mu          sync.RWMutex
	aesKey      []byte
	hasKey      bool
	fallbackKey []byte
)

func init() {
	Init("jobcon-embedded-default-key-v1")
}

// Init initializes the encryption key. If keyString is provided, it is hashed with SHA-256
// to produce a deterministic 32-byte AES-256 key.
func Init(keyString string) {
	mu.Lock()
	defer mu.Unlock()

	trimmed := strings.TrimSpace(keyString)
	if trimmed == "" {
		aesKey = nil
		hasKey = false
		return
	}

	h := sha256.Sum256([]byte(trimmed))
	aesKey = h[:]
	hasKey = true
}

// IsInitialized returns true if a master key has been configured.
func IsInitialized() bool {
	mu.RLock()
	defer mu.RUnlock()
	return hasKey
}

// Encrypt encrypts plaintext using AES-256-GCM and returns a string with format "enc:v1:<base64(nonce+ciphertext)>".
// If plaintext is empty, it returns an empty string without encryption.
// If no encryption key was initialized, it returns an error.
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	mu.RLock()
	key := aesKey
	ready := hasKey
	mu.RUnlock()

	if !ready || len(key) != 32 {
		return "", errors.New("encryption key not initialized in configuration")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefixV1 + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a string. If the value does not have the "enc:v1:" prefix,
// it is assumed to be legacy unencrypted data and returned as-is (graceful fallback).
func Decrypt(val string) (string, error) {
	if val == "" {
		return "", nil
	}

	if !strings.HasPrefix(val, prefixV1) {
		// Legacy plain-text fallback
		return val, nil
	}

	mu.RLock()
	key := aesKey
	ready := hasKey
	mu.RUnlock()

	if !ready || len(key) != 32 {
		return "", errors.New("cannot decrypt: encryption key not initialized in configuration")
	}

	rawB64 := strings.TrimPrefix(val, prefixV1)
	data, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return "", fmt.Errorf("invalid base64 encrypted payload: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key or corrupted data): %w", err)
	}

	return string(plaintext), nil
}
