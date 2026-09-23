// Copyright 2026 The Go Authors and webtransport-h2-go contributors.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// webtransport-h2-go: WebTransport-over-HTTP/2 SETTINGS (draft-ietf-webtrans-http2-15).

package http2

import (
	"context"
	"sync"
)

// Draft-ietf-webtrans-http2-15 §11.2 setting identifiers.
const (
	SettingWTEnabled                       SettingID = 0x2b60
	SettingWTInitialMaxData                SettingID = 0x2b61
	SettingWTInitialMaxStreamDataUni       SettingID = 0x2b62
	SettingWTInitialMaxStreamDataBidiLocal SettingID = 0x2b63
	SettingWTInitialMaxStreamsUni          SettingID = 0x2b64
	SettingWTInitialMaxStreamsBidi         SettingID = 0x2b65
	SettingWTInitialMaxStreamDataBidiRemote SettingID = 0x2b66
)

func init() {
	settingName[SettingWTEnabled] = "WT_ENABLED"
	settingName[SettingWTInitialMaxData] = "WT_INITIAL_MAX_DATA"
	settingName[SettingWTInitialMaxStreamDataUni] = "WT_INITIAL_MAX_STREAM_DATA_UNI"
	settingName[SettingWTInitialMaxStreamDataBidiLocal] = "WT_INITIAL_MAX_STREAM_DATA_BIDI_LOCAL"
	settingName[SettingWTInitialMaxStreamsUni] = "WT_INITIAL_MAX_STREAMS_UNI"
	settingName[SettingWTInitialMaxStreamsBidi] = "WT_INITIAL_MAX_STREAMS_BIDI"
	settingName[SettingWTInitialMaxStreamDataBidiRemote] = "WT_INITIAL_MAX_STREAM_DATA_BIDI_REMOTE"
}

// WebTransportSettings holds draft-ietf-webtrans-http2-15 SETTINGS values.
//
// When used as Server.WebTransport or Transport.WebTransport, a non-nil value
// causes these parameters to be included in the connection's initial SETTINGS
// frame. Zero-valued limits are still sent (the draft default is 0).
//
// When returned as peer settings, fields reflect the most recently received
// values (defaults are 0 if the peer omitted a parameter).
type WebTransportSettings struct {
	// Enabled is SETTINGS_WT_ENABLED. Servers should set this to true.
	// Clients typically leave this false; they signal support via CONNECT.
	Enabled bool

	InitialMaxData                  uint32
	InitialMaxStreamDataUni         uint32
	InitialMaxStreamDataBidiLocal   uint32
	InitialMaxStreamDataBidiRemote  uint32
	InitialMaxStreamsUni            uint32
	InitialMaxStreamsBidi           uint32
}

// DefaultWebTransportSettings returns generous POC defaults suitable for
// interop (non-zero flow-control credit and WT_ENABLED=1).
func DefaultWebTransportSettings() *WebTransportSettings {
	const maxData = 1 << 20
	const maxStreams = 100
	return &WebTransportSettings{
		Enabled:                        true,
		InitialMaxData:                 maxData,
		InitialMaxStreamDataUni:        maxData,
		InitialMaxStreamDataBidiLocal:  maxData,
		InitialMaxStreamDataBidiRemote: maxData,
		InitialMaxStreamsUni:           maxStreams,
		InitialMaxStreamsBidi:          maxStreams,
	}
}

// DefaultClientWebTransportSettings is like DefaultWebTransportSettings but
// with Enabled=false. Clients signal WebTransport support via the CONNECT
// :protocol token rather than SETTINGS_WT_ENABLED.
func DefaultClientWebTransportSettings() *WebTransportSettings {
	s := DefaultWebTransportSettings()
	s.Enabled = false
	return s
}

func (s *WebTransportSettings) settings() []Setting {
	if s == nil {
		return nil
	}
	out := make([]Setting, 0, 7)
	if s.Enabled {
		out = append(out, Setting{ID: SettingWTEnabled, Val: 1})
	}
	out = append(out,
		Setting{ID: SettingWTInitialMaxData, Val: s.InitialMaxData},
		Setting{ID: SettingWTInitialMaxStreamDataUni, Val: s.InitialMaxStreamDataUni},
		Setting{ID: SettingWTInitialMaxStreamDataBidiLocal, Val: s.InitialMaxStreamDataBidiLocal},
		Setting{ID: SettingWTInitialMaxStreamDataBidiRemote, Val: s.InitialMaxStreamDataBidiRemote},
		Setting{ID: SettingWTInitialMaxStreamsUni, Val: s.InitialMaxStreamsUni},
		Setting{ID: SettingWTInitialMaxStreamsBidi, Val: s.InitialMaxStreamsBidi},
	)
	return out
}

// peerWebTransportSettings tracks SETTINGS received from the remote endpoint.
type peerWebTransportSettings struct {
	mu sync.Mutex
	WebTransportSettings
}

func (p *peerWebTransportSettings) apply(s Setting) (handled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch s.ID {
	case SettingWTEnabled:
		p.Enabled = s.Val == 1
	case SettingWTInitialMaxData:
		p.InitialMaxData = s.Val
	case SettingWTInitialMaxStreamDataUni:
		p.InitialMaxStreamDataUni = s.Val
	case SettingWTInitialMaxStreamDataBidiLocal:
		p.InitialMaxStreamDataBidiLocal = s.Val
	case SettingWTInitialMaxStreamDataBidiRemote:
		p.InitialMaxStreamDataBidiRemote = s.Val
	case SettingWTInitialMaxStreamsUni:
		p.InitialMaxStreamsUni = s.Val
	case SettingWTInitialMaxStreamsBidi:
		p.InitialMaxStreamsBidi = s.Val
	default:
		return false
	}
	return true
}

func (p *peerWebTransportSettings) snapshot() WebTransportSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.WebTransportSettings
}

// PeerWebTransportSettingsContextKey is the context key for peer WebTransport
// SETTINGS on server-side requests. Value type is WebTransportSettings.
var PeerWebTransportSettingsContextKey = &webTransportPeerSettingsContextKey{}

type webTransportPeerSettingsContextKey struct{}

// PeerWebTransportSettingsFromContext returns peer WebTransport SETTINGS
// associated with an HTTP/2 server request context, if present.
func PeerWebTransportSettingsFromContext(ctx context.Context) (WebTransportSettings, bool) {
	v, ok := ctx.Value(PeerWebTransportSettingsContextKey).(WebTransportSettings)
	return v, ok
}
