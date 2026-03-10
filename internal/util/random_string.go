package util

import (
	"crypto/rand"
	"fmt"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GenerateString(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("invalid length: %d, must be positive", n)
	}
	b := make([]byte, n)
	charsetLength := byte(len(charset))

	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	for i := range b {
		b[i] = charset[int(b[i])%int(charsetLength)]
	}

	return string(b), nil
}
