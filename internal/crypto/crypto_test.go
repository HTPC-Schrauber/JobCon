package crypto

import (
	"strings"
	"testing"
)

func TestCrypto_EncryptDecrypt(t *testing.T) {
	Init("super-secret-master-key-12345")

	plaintext := "SuperSecretNexusPassword!#$123"
	encrypted, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	if !strings.HasPrefix(encrypted, "enc:v1:") {
		t.Fatalf("expected encrypted string to start with 'enc:v1:', got %q", encrypted)
	}

	if encrypted == plaintext {
		t.Fatalf("expected ciphertext to differ from plaintext")
	}

	// Encrypting the same plaintext again should yield a different ciphertext (random IV)
	encrypted2, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("second encryption failed: %v", err)
	}
	if encrypted == encrypted2 {
		t.Errorf("expected different ciphertexts due to random nonce, got identical")
	}

	// Decrypt
	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}

	decrypted2, err := Decrypt(encrypted2)
	if err != nil {
		t.Fatalf("second decryption failed: %v", err)
	}
	if decrypted2 != plaintext {
		t.Errorf("expected %q, got %q", plaintext, decrypted2)
	}
}

func TestCrypto_Empty(t *testing.T) {
	Init("any-key")

	enc, err := Encrypt("")
	if err != nil {
		t.Fatalf("expected nil err for empty, got %v", err)
	}
	if enc != "" {
		t.Errorf("expected empty ciphertext for empty plaintext, got %q", enc)
	}

	dec, err := Decrypt("")
	if err != nil {
		t.Fatalf("expected nil err for empty, got %v", err)
	}
	if dec != "" {
		t.Errorf("expected empty plaintext for empty ciphertext, got %q", dec)
	}
}

func TestCrypto_LegacyPlaintextFallback(t *testing.T) {
	Init("test-key")

	legacyPassword := "mypassword123"
	decrypted, err := Decrypt(legacyPassword)
	if err != nil {
		t.Fatalf("legacy decryption failed: %v", err)
	}
	if decrypted != legacyPassword {
		t.Errorf("expected legacy fallback to return identical plaintext %q, got %q", legacyPassword, decrypted)
	}
}

func TestCrypto_WrongKeyFails(t *testing.T) {
	Init("correct-key")
	enc, err := Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	Init("wrong-key")
	_, err = Decrypt(enc)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}
}

func TestCrypto_UninitializedErrors(t *testing.T) {
	Init("")
	defer Init("jobcon-embedded-default-key-v1")
	_, err := Encrypt("test")
	if err == nil {
		t.Fatal("expected error encrypting without key, got nil")
	}

	_, err = Decrypt("enc:v1:YWJj")
	if err == nil {
		t.Fatal("expected error decrypting without key, got nil")
	}
}
