package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Session error codes from draft-frindell-webtrans-devious-baton-00 §4.6.2.
const (
	errDAYAMN uint32 = 0x01 // insufficient stream credit
	errBRUH   uint32 = 0x02 // malformed baton message
	errSUS    uint32 = 0x03 // unexpected baton message
	errBORED  uint32 = 0x04 // tired of waiting for credit / next message
)

const batonPath = "/webtransport/devious-baton"

type batonConfig struct {
	Version int
	Baton   byte // 0 means server chooses randomly (only meaningful before selection)
	Count   int
	Padding int // padding bytes we send (kept small until flow control exists)
}

func defaultConfig() batonConfig {
	return batonConfig{
		Version: 0,
		Count:   1,
		Padding: 0,
	}
}

func parseBatonQuery(q url.Values) (batonConfig, error) {
	cfg := defaultConfig()

	if v := q.Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return cfg, fmt.Errorf("invalid version %q", v)
		}
		if n != 0 {
			return cfg, fmt.Errorf("unsupported version %d", n)
		}
		cfg.Version = n
	}

	if v := q.Get("baton"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 255 {
			return cfg, fmt.Errorf("invalid baton %q (want 1–255)", v)
		}
		cfg.Baton = byte(n)
	}

	if v := q.Get("count"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("invalid count %q", v)
		}
		cfg.Count = n
	}

	return cfg, nil
}

func (c batonConfig) query() url.Values {
	q := url.Values{}
	q.Set("version", strconv.Itoa(c.Version))
	q.Set("count", strconv.Itoa(c.Count))
	if c.Baton != 0 {
		q.Set("baton", strconv.Itoa(int(c.Baton)))
	}
	return q
}

func rejectBadRequest(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}
