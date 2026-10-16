// Package uuid generates RFC 4122 UUIDs: random (version 4) and name-based SHA-1 (version 5).
package uuid

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

// UUID is a raw 16-byte UUID.
type UUID [16]byte

// NamespaceURL is the RFC 4122 namespace for URLs.
var NamespaceURL = MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

// New returns a random version 4 UUID in canonical form.
func New() (string, error) {
	var b UUID
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return b.String(), nil
}

// NewV5 returns the version 5 UUID of name in namespace ns: the same input always gives the same id.
func NewV5(ns UUID, name string) UUID {
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	var b UUID
	copy(b[:], h.Sum(nil))
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return b
}

// Parse reads the canonical 36-character form.
func Parse(s string) (UUID, error) {
	var b UUID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return b, fmt.Errorf("invalid uuid")
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(raw) != 16 {
		return b, fmt.Errorf("invalid uuid")
	}
	copy(b[:], raw)
	return b, nil
}

// MustParse is Parse for constants.
func MustParse(s string) UUID {
	b, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return b
}

func (b UUID) String() string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
