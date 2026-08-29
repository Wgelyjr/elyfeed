package auth

import (
	"encoding/hex"
	"testing"
)

func TestNewToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if len(tok) != 64 {
			t.Fatalf("token length = %d, want 64 hex chars", len(tok))
		}
		if _, err := hex.DecodeString(tok); err != nil {
			t.Fatalf("token %q is not hex: %v", tok, err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

func TestHashToken(t *testing.T) {
	a := HashToken("some-raw-token")
	b := HashToken("some-raw-token")
	c := HashToken("another-raw-token")
	if a != b {
		t.Fatal("HashToken is not deterministic")
	}
	if a == c {
		t.Fatal("HashToken collides on different inputs")
	}
	if len(a) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("hash %q is not hex: %v", a, err)
	}
}
