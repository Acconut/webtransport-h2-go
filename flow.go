package wth2

import "sync"

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
}

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
