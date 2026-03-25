package wth2

import "fmt"

// CapsuleType is a WebTransport-over-HTTP/2 capsule type (draft-ietf-webtrans-http2 §6).
// WT_CLOSE_SESSION and WT_DRAIN_SESSION values come from draft-ietf-webtrans-http3 (referenced by the HTTP/2 draft).
type CapsuleType uint64

const (
	CapsulePadding CapsuleType = 0x190B4D38

	CapsuleWTResetStream CapsuleType = 0x190B4D39
	CapsuleWTStopSending CapsuleType = 0x190B4D3A

	CapsuleWTStream    CapsuleType = 0x190B4D3B
	CapsuleWTStreamFin CapsuleType = 0x190B4D3C

	CapsuleWTMaxData       CapsuleType = 0x190B4D3D
	CapsuleWTMaxStreamData CapsuleType = 0x190B4D3E

	CapsuleWTMaxStreamsBidi CapsuleType = 0x190B4D3F
	CapsuleWTMaxStreamsUni  CapsuleType = 0x190B4D40

	CapsuleWTDataBlocked       CapsuleType = 0x190B4D41
	CapsuleWTStreamDataBlocked CapsuleType = 0x190B4D42

	CapsuleWTStreamsBlockedBidi CapsuleType = 0x190B4D43
	CapsuleWTStreamsBlockedUni  CapsuleType = 0x190B4D44

	CapsuleDatagram CapsuleType = 0x00

	CapsuleWTCloseSession CapsuleType = 0x2843
	CapsuleWTDrainSession CapsuleType = 0x78ae
)

// String returns the IANA-style capsule name, or unknown(0x…) for unrecognized values.
func (c CapsuleType) String() string {
	switch c {
	case CapsulePadding:
		return "PADDING"
	case CapsuleWTResetStream:
		return "WT_RESET_STREAM"
	case CapsuleWTStopSending:
		return "WT_STOP_SENDING"
	case CapsuleWTStream:
		return "WT_STREAM"
	case CapsuleWTStreamFin:
		return "WT_STREAM_FIN"
	case CapsuleWTMaxData:
		return "WT_MAX_DATA"
	case CapsuleWTMaxStreamData:
		return "WT_MAX_STREAM_DATA"
	case CapsuleWTMaxStreamsBidi:
		return "WT_MAX_STREAMS_BIDI"
	case CapsuleWTMaxStreamsUni:
		return "WT_MAX_STREAMS_UNI"
	case CapsuleWTDataBlocked:
		return "WT_DATA_BLOCKED"
	case CapsuleWTStreamDataBlocked:
		return "WT_STREAM_DATA_BLOCKED"
	case CapsuleWTStreamsBlockedBidi:
		return "WT_STREAMS_BLOCKED_BIDI"
	case CapsuleWTStreamsBlockedUni:
		return "WT_STREAMS_BLOCKED_UNI"
	case CapsuleDatagram:
		return "DATAGRAM"
	case CapsuleWTCloseSession:
		return "WT_CLOSE_SESSION"
	case CapsuleWTDrainSession:
		return "WT_DRAIN_SESSION"
	default:
		return fmt.Sprintf("unknown(0x%X)", uint64(c))
	}
}
