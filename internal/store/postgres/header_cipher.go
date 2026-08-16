package postgres

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type encryptedHeaders struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type HeaderCipher struct {
	aead cipher.AEAD
}

func NewHeaderCipher(encodedKey string) (*HeaderCipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode RC_HEADER_ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("RC_HEADER_ENCRYPTION_KEY must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM cipher: %w", err)
	}
	return &HeaderCipher{aead: aead}, nil
}

func (c *HeaderCipher) Encrypt(taskID uuid.UUID, headers map[string]string) (json.RawMessage, error) {
	plaintext, err := json.Marshal(headers)
	if err != nil {
		return nil, fmt.Errorf("marshal target headers: %w", err)
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate header encryption nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, taskID[:])
	envelope, err := json.Marshal(encryptedHeaders{
		Version:    1,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal encrypted target headers: %w", err)
	}
	return envelope, nil
}

func (c *HeaderCipher) Decrypt(taskID uuid.UUID, payload json.RawMessage) (map[string]string, error) {
	var envelope encryptedHeaders
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode encrypted target headers: %w", err)
	}
	if envelope.Version != 1 {
		return nil, fmt.Errorf("unsupported target header encryption version %d", envelope.Version)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != c.aead.NonceSize() {
		return nil, errors.New("decode target header encryption nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode target header ciphertext: %w", err)
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, taskID[:])
	if err != nil {
		return nil, fmt.Errorf("decrypt target headers: %w", err)
	}
	var headers map[string]string
	if err := json.Unmarshal(plaintext, &headers); err != nil {
		return nil, fmt.Errorf("decode target headers: %w", err)
	}
	return headers, nil
}
