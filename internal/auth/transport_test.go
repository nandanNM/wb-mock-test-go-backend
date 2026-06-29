package auth

import (
	"io"
	"log/slog"
	"testing"

	"backend/internal/config"
)

func testTransport(t *testing.T) *Transport {
	t.Helper()
	return NewTransport(config.AuthConfig{CSRFHMACKey: "test-csrf-key"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCSRFValidate(t *testing.T) {
	tr := testTransport(t)
	token := tr.issueCSRF()

	if !tr.ValidateCSRF(token, token) {
		t.Fatal("matching cookie+header with valid HMAC should pass")
	}
	if tr.ValidateCSRF(token, "") {
		t.Fatal("missing header should fail")
	}
	if tr.ValidateCSRF(token, token+"x") {
		t.Fatal("mismatched cookie/header should fail")
	}
	// Tampered MAC: same nonce, different signature.
	nonce := "deadbeefdeadbeefdeadbeefdeadbeef"
	forged := nonce + ".0000000000000000000000000000000000000000000000000000000000000000"
	if tr.ValidateCSRF(forged, forged) {
		t.Fatal("forged HMAC should fail")
	}
}

func TestCSRFKeyIsolation(t *testing.T) {
	a := testTransport(t)
	b := NewTransport(config.AuthConfig{CSRFHMACKey: "different-key"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	token := a.issueCSRF()
	if b.ValidateCSRF(token, token) {
		t.Fatal("token signed with key A must not validate under key B")
	}
}
