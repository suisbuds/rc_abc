package postgres

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHeaderCipherRoundTrip(t *testing.T) {
	cipher := newTestHeaderCipher(t, 1)
	taskID := uuid.New()
	headers := map[string]string{"Authorization": "Bearer supplier-secret", "X-Event-Type": "paid"}

	encrypted, err := cipher.Encrypt(taskID, headers)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if strings.Contains(string(encrypted), "supplier-secret") {
		t.Fatal("Encrypt() output contains plaintext secret")
	}
	decrypted, err := cipher.Decrypt(taskID, encrypted)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if decrypted["Authorization"] != headers["Authorization"] {
		t.Fatalf("Authorization = %q, want %q", decrypted["Authorization"], headers["Authorization"])
	}
}

func TestHeaderCipherRejectsWrongTaskAndTampering(t *testing.T) {
	cipher := newTestHeaderCipher(t, 2)
	taskID := uuid.New()
	encrypted, err := cipher.Encrypt(taskID, map[string]string{"Authorization": "secret"})
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	if _, err := cipher.Decrypt(uuid.New(), encrypted); err == nil {
		t.Fatal("Decrypt() with another task ID error = nil, want authentication error")
	}

	var envelope encryptedHeaders
	if err := json.Unmarshal(encrypted, &envelope); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	ciphertext[0] ^= 1
	envelope.Ciphertext = base64.StdEncoding.EncodeToString(ciphertext)
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if _, err := cipher.Decrypt(taskID, tampered); err == nil {
		t.Fatal("Decrypt() tampered ciphertext error = nil, want authentication error")
	}
}

func TestHeaderCipherRejectsInvalidKey(t *testing.T) {
	if _, err := NewHeaderCipher(base64.StdEncoding.EncodeToString([]byte("too-short"))); err == nil {
		t.Fatal("NewHeaderCipher() error = nil, want key length error")
	}
}

func newTestHeaderCipher(t *testing.T, fill byte) *HeaderCipher {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = fill
	}
	cipher, err := NewHeaderCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewHeaderCipher() error = %v", err)
	}
	return cipher
}
