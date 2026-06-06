package memory

import (
	"encoding/base64"
	"errors"
	"strings"
)

const edgeIDNulSep = "\x00"

// EncodeEdgeID produces an opaque, URL-safe identifier for an edge
// between two graph nodes. Callers MUST treat the returned string as
// opaque and never parse it - the encoding is allowed to change.
func EncodeEdgeID(from, to string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(from + edgeIDNulSep + to))
}

// DecodeEdgeID extracts the (from, to) pair from an opaque edge ID
// produced by EncodeEdgeID. Returns an error if the input is not a
// well-formed encoded edge ID.
func DecodeEdgeID(id string) (from, to string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", "", err
	}
	s := string(raw)
	i := strings.Index(s, edgeIDNulSep)
	if i < 0 {
		return "", "", errors.New("edge id: missing separator")
	}
	return s[:i], s[i+len(edgeIDNulSep):], nil
}
