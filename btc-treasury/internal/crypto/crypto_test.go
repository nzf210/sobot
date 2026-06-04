package crypto

import (
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	plaintext := "0xabc123def456"
	password := "test_password"

	encrypted, err := Encrypt(plaintext, password)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := Decrypt(encrypted, password)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestWrongPasswordFails(t *testing.T) {
	plaintext := "secret"
	encrypted, err := Encrypt(plaintext, "correct")
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	_, err = Decrypt(encrypted, "wrong")
	if err == nil {
		t.Error("expected decryption to fail with wrong password, but it succeeded")
	}
}

func TestShortDataFails(t *testing.T) {
	_, err := Decrypt([]byte("short"), "pw")
	if err == nil {
		t.Error("expected decryption to fail with short input, but it succeeded")
	}
}
