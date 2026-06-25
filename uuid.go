package groundcover

import (
	"crypto/rand"
	"encoding/hex"
)

// newUUID returns a random RFC 4122 version 4 UUID string. On the unlikely
// event that the system RNG fails, it returns a zero UUID rather than panicking.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}

// newSpanID returns a random 8-byte hex identifier (16 hex chars), matching the
// RUM event id/spanId shape.
func newSpanID() string { return randHex(8) }

// newTraceID returns a random 16-byte hex identifier (32 hex chars).
func newTraceID() string { return randHex(16) }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = 0
		}
	}
	return hex.EncodeToString(b)
}
