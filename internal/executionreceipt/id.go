package executionreceipt

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// NewRunID returns a cryptographically random RFC 4122 UUID version 4.
func NewRunID() (string, error) {
	return newRunID(rand.Reader)
}

func newRunID(source io.Reader) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(source, raw[:]); err != nil {
		return "", fmt.Errorf("generate execution receipt run ID: %w", err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80

	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func validateRunID(value string) error {
	if !runIDPattern.MatchString(value) {
		return fmt.Errorf("run_id must be a canonical lowercase UUID v4")
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil {
		return fmt.Errorf("decode run_id: %w", err)
	}
	if raw[6]>>4 != 4 {
		return fmt.Errorf("run_id must use UUID version 4")
	}
	if raw[8]&0xc0 != 0x80 {
		return fmt.Errorf("run_id must use RFC 4122 variant")
	}
	return nil
}
