package auth

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash = %q, want argon2id PHC format", hash)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("verify correct password: want true")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("verify wrong password: want false")
	}
}

func TestHashIsSalted(t *testing.T) {
	a, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are identical; salt missing")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, hash := range []string{
		"",
		"$argon2id$v=19$m=65536,t=3,p=4$",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!not-base64$abc",
		"pbkdf2$whatever",
		"$argon2id$short",
	} {
		if VerifyPassword("password", hash) {
			t.Errorf("verify(%q): want false", hash)
		}
	}
}
