package main

import (
	"net/url"
	"testing"
)

func TestEncodeDecodeBaton(t *testing.T) {
	msg := encodeBaton([]byte{0xaa, 0xbb}, 42)
	pad, baton, err := decodeBaton(msg)
	if err != nil {
		t.Fatal(err)
	}
	if baton != 42 {
		t.Fatalf("baton=%d", baton)
	}
	if string(pad) != string([]byte{0xaa, 0xbb}) {
		t.Fatalf("padding=%v", pad)
	}
}

func TestDecodeBatonEmpty(t *testing.T) {
	if _, _, err := decodeBaton(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestDecodeBatonTruncated(t *testing.T) {
	msg := encodeBaton(make([]byte, 5), 1)
	if _, _, err := decodeBaton(msg[:len(msg)-1]); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBatonQuery(t *testing.T) {
	cfg, err := parseBatonQuery(url.Values{
		"version": {"0"},
		"baton":   {"42"},
		"count":   {"3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Baton != 42 || cfg.Count != 3 || cfg.Version != 0 {
		t.Fatalf("%+v", cfg)
	}
}

func TestParseBatonQueryRejects(t *testing.T) {
	cases := []string{"version=1", "baton=0", "baton=256", "count=0", "count=-1"}
	for _, raw := range cases {
		q, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseBatonQuery(q); err == nil {
			t.Fatalf("expected reject for %s", raw)
		}
	}
}
