package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP-recommended minimums).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB, expressed in KiB
	argonThreads = 4
	saltSize     = 16
	hashSize     = 32
)

// HashPassword hashes a plaintext password with argon2id and returns an
// encoded, self-describing string (PHC format).
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	dk := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, hashSize)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		enc.EncodeToString(salt), enc.EncodeToString(dk)), nil
}

// VerifyPassword reports whether the plaintext password matches the encoded
// hash. It is constant-time with respect to the password.
func VerifyPassword(password, encoded string) bool {
	// PHC format: $argon2id$v=<ver>$m=..,t=..,p=..$<salt>$<hash> — the leading
	// '$' makes the split produce an empty first field.
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false
	}
	var m, t, p uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	if m == 0 || t == 0 || p == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != hashSize {
		return false
	}
	dk := argon2.IDKey([]byte(password), salt, t, m, uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(dk, want) == 1
}
