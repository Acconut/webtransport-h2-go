package wth2

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/quic-go/quic-go/quicvarint"
)

// peerInitialLimits is the peer's SETTINGS_WT_INITIAL_MAX_* grant for data
// and streams this endpoint may send. Draft-ietf-webtrans-http2-15 §4.3.1.
// Omitted settings are zero: the peer has granted no credit yet.
type peerInitialLimits struct {
	maxData              uint64
	maxStreamsUni        uint64
	maxStreamsBidi       uint64
	streamDataUni        uint64
	streamDataBidiLocal  uint64 // data we send on bidirectional streams the peer opened
	streamDataBidiRemote uint64 // data we send on bidirectional streams we open
}

// sendLimits tracks the peer's current send grant.
// active is false until a peer snapshot is installed; in-memory sessions
// that never see SETTINGS stay inactive and are not limited.
type sendLimits struct {
	mu     sync.Mutex
	active bool

	maxData        uint64
	maxStreamsUni  uint64
	maxStreamsBidi uint64

	streamDataUni        uint64
	streamDataBidiLocal  uint64
	streamDataBidiRemote uint64

	// streamMax is the current max WT_STREAM payload for a stream ID.
	// New streams copy the matching initial value. A later capsule may
	// raise an entry before the stream object exists.
	streamMax map[uint64]uint64

	dataSent   uint64
	streamSent map[uint64]uint64
	openedUni  uint64
	openedBidi uint64
}

// ErrSendLimit is returned when opening a stream or writing stream data
// would exceed the peer's current grant. The operation sends nothing.
var ErrSendLimit = errors.New("peer send limit exhausted")

func (s *Session) adoptPeerSendLimits(peer peerInitialLimits) {
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	s.limits.active = true
	s.limits.maxData = peer.maxData
	s.limits.maxStreamsUni = peer.maxStreamsUni
	s.limits.maxStreamsBidi = peer.maxStreamsBidi
	s.limits.streamDataUni = peer.streamDataUni
	s.limits.streamDataBidiLocal = peer.streamDataBidiLocal
	s.limits.streamDataBidiRemote = peer.streamDataBidiRemote
}

// trackSendStream copies the peer's initial per-stream data limit onto id.
// Peer-initiated unidirectional streams are send-only for the peer, so they
// get no local send limit. An entry already set (for example by WT_MAX_STREAM_DATA
// before the stream was opened) is left in place.
func (s *Session) trackSendStream(id uint64) {
	if !isBidirectionalStream(id) && !s.isOurStream(id) {
		return
	}
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	s.ensureStreamSendLimitLocked(id)
}

// allocLocalStream reserves a stream-count credit, when limits are active,
// and allocates the next local stream ID. A refused open does not consume
// an ID.
func (s *Session) allocLocalStream(bidi bool) (uint64, error) {
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	if s.limits.active {
		opened := s.limits.openedUni
		max := s.limits.maxStreamsUni
		kind := "unidirectional streams"
		if bidi {
			opened = s.limits.openedBidi
			max = s.limits.maxStreamsBidi
			kind = "bidirectional streams"
		}
		if opened >= max {
			return 0, fmt.Errorf("%w: %s", ErrSendLimit, kind)
		}
	}
	id := s.nextStreamID(bidi)
	if s.limits.active {
		if bidi {
			s.limits.openedBidi++
		} else {
			s.limits.openedUni++
		}
		s.ensureStreamSendLimitLocked(id)
	}
	return id, nil
}

func (s *Session) ensureStreamSendLimitLocked(id uint64) {
	if !s.limits.active {
		return
	}
	if s.limits.streamMax == nil {
		s.limits.streamMax = make(map[uint64]uint64)
	}
	if _, ok := s.limits.streamMax[id]; ok {
		return
	}
	s.limits.streamMax[id] = s.initialStreamSendLimitLocked(id)
}

// reserveSendData charges n payload bytes against the session and stream
// grants. A write that does not fit is rejected whole; nothing is charged.
func (s *Session) reserveSendData(id uint64, n int) error {
	if n == 0 {
		return nil
	}
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	if !s.limits.active {
		return nil
	}
	add := uint64(n)
	if s.limits.dataSent > s.limits.maxData || add > s.limits.maxData-s.limits.dataSent {
		return fmt.Errorf("%w: session data", ErrSendLimit)
	}
	var maxStream uint64
	if s.limits.streamMax != nil {
		maxStream = s.limits.streamMax[id]
	}
	var sent uint64
	if s.limits.streamSent != nil {
		sent = s.limits.streamSent[id]
	}
	if sent > maxStream || add > maxStream-sent {
		return fmt.Errorf("%w: stream data", ErrSendLimit)
	}
	s.limits.dataSent += add
	if s.limits.streamSent == nil {
		s.limits.streamSent = make(map[uint64]uint64)
	}
	s.limits.streamSent[id] = sent + add
	return nil
}

func (s *Session) refundSendData(id uint64, n int) {
	if n <= 0 {
		return
	}
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	if !s.limits.active {
		return
	}
	sub := uint64(n)
	if s.limits.dataSent >= sub {
		s.limits.dataSent -= sub
	}
	if s.limits.streamSent != nil && s.limits.streamSent[id] >= sub {
		s.limits.streamSent[id] -= sub
	}
}

// initialStreamSendLimitLocked is the SETTINGS value that applies to data
// this endpoint sends on id. The names are from the peer's point of view:
// "local" streams are ones the peer opened, "remote" streams are ones we open.
func (s *Session) initialStreamSendLimitLocked(id uint64) uint64 {
	if !isBidirectionalStream(id) {
		return s.limits.streamDataUni
	}
	if s.isOurStream(id) {
		return s.limits.streamDataBidiRemote
	}
	return s.limits.streamDataBidiLocal
}

// FlowControlError is a session error raised when the peer decreases a send
// limit or sends a stream limit above 2^60. Draft-ietf-webtrans-http2-15
// calls this WT_FLOW_CONTROL_ERROR.
type FlowControlError struct {
	Reason string
}

func (e *FlowControlError) Error() string {
	return "webtransport flow control error: " + e.Reason
}

// maxStreamCount is the largest WT_MAX_STREAMS value. Stream IDs cannot
// encode a count above 2^60.
const maxStreamCount = 1 << 60

func (s *Session) handleSendLimitCapsule(typ CapsuleType, content io.Reader) error {
	switch typ {
	case CapsuleWTMaxData:
		max, err := readCapsuleVarints(content, 1)
		if err != nil {
			return err
		}
		s.log.Printf("received capsule %s max=%d", typ, max[0])
		return s.raiseMaxData(max[0])
	case CapsuleWTMaxStreamData:
		fields, err := readCapsuleVarints(content, 2)
		if err != nil {
			return err
		}
		s.log.Printf("received capsule %s stream_id=%d max=%d", typ, fields[0], fields[1])
		return s.raiseMaxStreamData(fields[0], fields[1])
	case CapsuleWTMaxStreamsBidi, CapsuleWTMaxStreamsUni:
		max, err := readCapsuleVarints(content, 1)
		if err != nil {
			return err
		}
		s.log.Printf("received capsule %s max=%d", typ, max[0])
		return s.raiseMaxStreams(typ == CapsuleWTMaxStreamsBidi, max[0])
	default:
		_, _ = io.Copy(io.Discard, content)
		return nil
	}
}

func readCapsuleVarints(content io.Reader, n int) ([]uint64, error) {
	r := quicvarint.NewReader(content)
	out := make([]uint64, n)
	for i := range out {
		v, err := quicvarint.Read(r)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	if _, err := io.Copy(io.Discard, content); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Session) raiseMaxData(max uint64) error {
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	s.limits.active = true
	if max < s.limits.maxData {
		return &FlowControlError{Reason: fmt.Sprintf("WT_MAX_DATA %d is below %d", max, s.limits.maxData)}
	}
	s.limits.maxData = max
	return nil
}

func (s *Session) raiseMaxStreamData(id, max uint64) error {
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	s.limits.active = true
	cur := s.initialStreamSendLimitLocked(id)
	if s.limits.streamMax != nil {
		if existing, ok := s.limits.streamMax[id]; ok {
			cur = existing
		}
	}
	if max < cur {
		return &FlowControlError{Reason: fmt.Sprintf("WT_MAX_STREAM_DATA %d for stream %d is below %d", max, id, cur)}
	}
	if s.limits.streamMax == nil {
		s.limits.streamMax = make(map[uint64]uint64)
	}
	s.limits.streamMax[id] = max
	return nil
}

func (s *Session) raiseMaxStreams(bidi bool, max uint64) error {
	if max > maxStreamCount {
		return &FlowControlError{Reason: fmt.Sprintf("WT_MAX_STREAMS %d exceeds 2^60", max)}
	}
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	s.limits.active = true
	cur := s.limits.maxStreamsUni
	if bidi {
		cur = s.limits.maxStreamsBidi
	}
	if max < cur {
		return &FlowControlError{Reason: fmt.Sprintf("WT_MAX_STREAMS %d is below %d", max, cur)}
	}
	if bidi {
		s.limits.maxStreamsBidi = max
	} else {
		s.limits.maxStreamsUni = max
	}
	return nil
}

func flowControlError(err error) *FlowControlError {
	var fc *FlowControlError
	if errors.As(err, &fc) {
		return fc
	}
	return nil
}
