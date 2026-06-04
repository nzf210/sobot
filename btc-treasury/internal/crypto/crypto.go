package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

const (
	saltLen = 16
	ivLen   = 12
	tagLen  = 16
)

// DeriveKey derives a 32-byte key from a password and salt using Scrypt.
func DeriveKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 16384, 8, 1, 32)
}

// Encrypt encrypts plaintext with password using AES-256-GCM.
// Output format: salt(16) || iv(12) || tag(16) || ciphertext
func Encrypt(plaintext string, password string) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	key, err := DeriveKey(password, salt)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate iv: %w", err)
	}

	sealed := aesgcm.Seal(nil, iv, []byte(plaintext), nil)
	if len(sealed) < tagLen {
		return nil, errors.New("encryption output too short")
	}

	ciphertextLen := len(sealed) - tagLen
	tag := sealed[ciphertextLen:]
	ciphertext := sealed[:ciphertextLen]

	output := make([]byte, 0, saltLen+ivLen+tagLen+len(ciphertext))
	output = append(output, salt...)
	output = append(output, iv...)
	output = append(output, tag...)
	output = append(output, ciphertext...)

	return output, nil
}

// Decrypt decrypts data encrypted by Encrypt.
// Input format: salt(16) || iv(12) || tag(16) || ciphertext
func Decrypt(encryptedData []byte, password string) (string, error) {
	minLen := saltLen + ivLen + tagLen
	if len(encryptedData) < minLen+1 {
		return "", errors.New("encrypted data too short")
	}

	salt := encryptedData[:saltLen]
	iv := encryptedData[saltLen : saltLen+ivLen]
	tag := encryptedData[saltLen+ivLen : minLen]
	ciphertext := encryptedData[minLen:]

	key, err := DeriveKey(password, salt)
	if err != nil {
		return "", fmt.Errorf("failed to derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Reconstruct Go AES-GCM input format: ciphertext || tag
	ciphertextWithTag := make([]byte, len(ciphertext)+tagLen)
	copy(ciphertextWithTag, ciphertext)
	copy(ciphertextWithTag[len(ciphertext):], tag)

	plaintext, err := aesgcm.Open(nil, iv, ciphertextWithTag, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong password?): %w", err)
	}

	return string(plaintext), nil
}
