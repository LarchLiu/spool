package relay

import (
	"bytes"
	"testing"
)

// These byte strings are the wire format. A host or relay that computes
// different bytes cannot register, so a change here is a protocol break.
func TestSignedProofLayoutsAreStable(t *testing.T) {
	challenge, token, origin, name := "challenge", "token", "https://relay.example.com", "home"
	for _, tc := range []struct {
		what string
		got  []byte
		want string
	}{
		{"native", proof(challenge, token), "spool-relay-v1\x00challenge\x00token"},
		{"unregister", unregisterProof(challenge, token), "spool-relay-unregister-v1\x00challenge\x00token"},
		{"shared-origin web", hostRegistrationProof(challenge, token, origin, "", 0), "spool-relay-web-v1\x00challenge\x00token\x00https://relay.example.com"},
		{"named enrollment", hostRegistrationProof(challenge, token, origin, name, 0), "spool-relay-register-v1\x00challenge\x00token\x00https://relay.example.com\x00home"},
		{"isolated web", hostRegistrationProof(challenge, token, origin, name, 2), "spool-relay-subdomain-v2\x00challenge\x00token\x00https://relay.example.com\x00home"},
	} {
		if string(tc.got) != tc.want {
			t.Errorf("%s layout changed:\n got %q\nwant %q", tc.what, tc.got, tc.want)
		}
	}
}

// A signature for one purpose must never verify as another.
func TestSignedProofLayoutsAreDistinct(t *testing.T) {
	challenge, token, origin, name := "challenge", "token", "https://relay.example.com", "home"
	layouts := [][]byte{
		proof(challenge, token),
		unregisterProof(challenge, token),
		hostRegistrationProof(challenge, token, origin, "", 0),
		hostRegistrationProof(challenge, token, origin, name, 0),
		hostRegistrationProof(challenge, token, origin, name, 2),
		hostRegistrationProof(challenge, token, "", name, 0),
	}
	for i, left := range layouts {
		for j, right := range layouts {
			if i != j && bytes.Equal(left, right) {
				t.Fatalf("layouts %d and %d are identical", i, j)
			}
		}
	}
}
