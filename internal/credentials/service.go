package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const EncryptionKeyEnv = "CONOPS_ENCRYPTION_KEY"
const EncryptionKeyFileEnv = "CONOPS_ENCRYPTION_KEY_FILE"

// Service encrypts/decrypts sensitive values for at-rest storage.
type Service struct {
	aead   cipher.AEAD
	source string
}

// NewServiceFromEnv initializes the encryption service.
// Priority:
// 1. CONOPS_ENCRYPTION_KEY (raw/base64, 32 bytes)
// 2. Key file (CONOPS_ENCRYPTION_KEY_FILE or default path), auto-generated on first run.
func NewServiceFromEnv(defaultKeyPath string) (*Service, error) {
	raw := strings.TrimSpace(os.Getenv(EncryptionKeyEnv))
	if raw != "" {
		key, err := parseKey(raw, EncryptionKeyEnv)
		if err != nil {
			return nil, err
		}
		return newServiceWithKey(key, "env:"+EncryptionKeyEnv)
	}

	keyPath := strings.TrimSpace(os.Getenv(EncryptionKeyFileEnv))
	if keyPath == "" {
		keyPath = defaultKeyPath
	}
	if keyPath == "" {
		return nil, fmt.Errorf("missing default encryption key path")
	}

	key, source, err := loadOrCreateKeyFile(keyPath)
	if err != nil {
		return nil, err
	}
	return newServiceWithKey(key, source)
}

func newServiceWithKey(key []byte, source string) (*Service, error) {
	defer zeroBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize encryption: %w", err)
	}

	return &Service{aead: aead, source: source}, nil
}

func parseKey(value, source string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, nil
	}

	if len(value) == 32 {
		return []byte(value), nil
	}

	return nil, fmt.Errorf("%s must be 32 raw bytes or base64 for 32 bytes", source)
}

func loadOrCreateKeyFile(path string) ([]byte, string, error) {
	existing, err := os.ReadFile(path)
	if err == nil {
		key, parseErr := parseKey(strings.TrimSpace(string(existing)), "key file "+path)
		if parseErr != nil {
			return nil, "", parseErr
		}
		return key, "file:" + path, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("failed reading key file: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if mkdirErr := os.MkdirAll(dir, 0700); mkdirErr != nil {
			return nil, "", fmt.Errorf("failed creating key dir: %w", mkdirErr)
		}
	}

	key := make([]byte, 32)
	if _, randErr := rand.Read(key); randErr != nil {
		return nil, "", fmt.Errorf("failed generating encryption key: %w", randErr)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"

	file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if createErr != nil {
		if errors.Is(createErr, os.ErrExist) {
			return loadOrCreateKeyFile(path)
		}
		return nil, "", fmt.Errorf("failed creating key file: %w", createErr)
	}
	if _, writeErr := file.WriteString(encoded); writeErr != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("failed writing key file: %w", writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		return nil, "", fmt.Errorf("failed closing key file: %w", closeErr)
	}

	return key, "file:" + path, nil
}

// Enabled reports whether encryption is configured.
func (s *Service) Enabled() bool {
	return s != nil && s.aead != nil
}

// KeySource returns where the encryption key was loaded from.
func (s *Service) KeySource() string {
	if s == nil {
		return ""
	}
	return s.source
}

// Encrypt seals plaintext.
func (s *Service) Encrypt(plaintext []byte) ([]byte, []byte, error) {
	if !s.Enabled() {
		return nil, nil, fmt.Errorf("credential encryption is disabled: %s is not set", EncryptionKeyEnv)
	}

	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("failed generating nonce: %w", err)
	}

	ciphertext := s.aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt opens encrypted data.
func (s *Service) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("credential encryption is disabled: %s is not set", EncryptionKeyEnv)
	}
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed decrypting credential: %w", err)
	}
	return plaintext, nil
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
