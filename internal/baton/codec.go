package baton

import (
	"fmt"

	"github.com/quic-go/quic-go/quicvarint"
)

// Baton message: padding length (varint) + padding + baton (1 byte).
// See draft-frindell-webtrans-devious-baton-00 §4.3.

func encodeBaton(padding []byte, baton byte) []byte {
	b := quicvarint.Append(nil, uint64(len(padding)))
	b = append(b, padding...)
	b = append(b, baton)
	return b
}

func decodeBaton(data []byte) (padding []byte, baton byte, err error) {
	if len(data) == 0 {
		return nil, 0, fmt.Errorf("empty baton message")
	}
	padLen, n, err := quicvarint.Parse(data)
	if err != nil {
		return nil, 0, fmt.Errorf("padding length: %w", err)
	}
	rest := data[n:]
	if uint64(len(rest)) < padLen+1 {
		return nil, 0, fmt.Errorf("truncated baton message: need %d padding + baton, have %d bytes", padLen, len(rest))
	}
	if uint64(len(rest)) != padLen+1 {
		return nil, 0, fmt.Errorf("trailing data after baton message")
	}
	return rest[:padLen], rest[padLen], nil
}
