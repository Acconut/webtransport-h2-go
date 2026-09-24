package wth2

// TestingStreamSendLimit reports the peer send limit copied onto id.
// The bool is false when this session is not applying peer limits or id
// has no send-side limit.
func (s *Session) TestingStreamSendLimit(id uint64) (uint64, bool) {
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	if !s.limits.active {
		return 0, false
	}
	v, ok := s.limits.streamMax[id]
	return v, ok
}
