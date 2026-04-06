package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Hash returns a hex-encoded SHA-256 hash of s.
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Greet produces a greeting message.
func Greet(name string) string {
	return fmt.Sprintf("hello, %s", strings.TrimSpace(name))
}

// Run is the entry point for benchmarking.
func Run() error {
	_ = Hash("benchmark")
	_ = Greet("world")
	return nil
}
