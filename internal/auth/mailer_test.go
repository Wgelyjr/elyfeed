package auth

import "testing"

func TestEnvelopeAddress(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"elyfeed <elyworksllc@gmail.com>", "elyworksllc@gmail.com"},
		{"<wgelyjr@gmail.com>", "wgelyjr@gmail.com"},
		{"wgelyjr@gmail.com", "wgelyjr@gmail.com"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := envelopeAddress(tc.in); got != tc.want {
			t.Errorf("envelopeAddress(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
