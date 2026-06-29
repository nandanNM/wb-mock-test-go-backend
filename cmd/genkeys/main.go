// Command genkeys prints fresh signing keys for .env:
//
//	AUTH_JWT_PRIVATE_KEY — base64 Ed25519 private key for access-token signing
//	CSRF_HMAC_KEY        — base64 random key for CSRF token HMAC
//
// Usage: make gen-keys   (or: go run ./cmd/genkeys)
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func main() {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	fmt.Println("AUTH_JWT_PRIVATE_KEY=" + base64.StdEncoding.EncodeToString(priv))

	csrf := make([]byte, 32)
	if _, err := rand.Read(csrf); err != nil {
		panic(err)
	}
	fmt.Println("CSRF_HMAC_KEY=" + base64.StdEncoding.EncodeToString(csrf))
}
