package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"backend/internal/config"
)

// Transport-layer constants.
const (
	RefreshCookieName = "refresh_token"
	CSRFCookieName    = "csrf_token"
	RefreshCookiePath = "/v1/auth"
	ClientTypeHeader  = "X-Client-Type"
	CSRFHeader        = "X-CSRF-Token"
)

// TokenResponse is the JSON body returned to clients after login/refresh.
// RefreshToken is populated only for native clients; web clients receive it as
// an HttpOnly cookie instead.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"` // access-token lifetime, seconds
	SessionID    string `json:"session_id"`
	RefreshToken string `json:"refresh_token,omitempty"` // native only
	CSRFToken    string `json:"csrf_token,omitempty"`    // web only (also set as cookie)
}

// Transport handles cookie-vs-body token delivery and CSRF tokens. It is the
// single place that branches on client type, keeping handlers uniform.
type Transport struct {
	cookieDomain string
	cookieSecure bool
	csrfKey      []byte
}

// NewTransport builds a Transport from config. If no CSRF key is configured it
// generates an ephemeral one (dev) and warns.
func NewTransport(cfg config.AuthConfig, log *slog.Logger) *Transport {
	key := []byte(cfg.CSRFHMACKey)
	if len(key) == 0 {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
		log.Warn("CSRF_HMAC_KEY not set — generated an ephemeral key; CSRF tokens will be invalid after restart. Set CSRF_HMAC_KEY in production.")
	}
	return &Transport{
		cookieDomain: cfg.CookieDomain,
		cookieSecure: cfg.CookieSecure,
		csrfKey:      key,
	}
}

// DetectClientType resolves the delivery style: an explicit X-Client-Type header
// wins; otherwise the presence of the refresh cookie implies a web client;
// default is native (bearer).
func DetectClientType(r *http.Request) ClientType {
	switch strings.ToLower(r.Header.Get(ClientTypeHeader)) {
	case "web":
		return ClientWeb
	case "native", "mobile":
		return ClientNative
	}
	if _, err := r.Cookie(RefreshCookieName); err == nil {
		return ClientWeb
	}
	return ClientNative
}

// Deliver writes the appropriate cookies (web) and returns the JSON body to send.
func (t *Transport) Deliver(w http.ResponseWriter, ct ClientType, tk Tokens) TokenResponse {
	resp := TokenResponse{
		AccessToken: tk.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(time.Until(tk.AccessExpiry).Seconds()),
		SessionID:   tk.SessionID,
	}
	if ct == ClientWeb {
		t.setCookie(w, RefreshCookieName, tk.RefreshToken, RefreshCookiePath, true, tk.RefreshExpiry)
		csrf := t.issueCSRF()
		// CSRF cookie is readable by JS (not HttpOnly) for the double-submit pattern.
		t.setCookie(w, CSRFCookieName, csrf, "/", false, tk.RefreshExpiry)
		resp.CSRFToken = csrf
	} else {
		resp.RefreshToken = tk.RefreshToken
	}
	return resp
}

// RefreshTokenFromCookie returns the refresh token carried in the cookie (web).
func (t *Transport) RefreshTokenFromCookie(r *http.Request) string {
	if c, err := r.Cookie(RefreshCookieName); err == nil {
		return c.Value
	}
	return ""
}

// ClearAuthCookies expires the refresh and CSRF cookies (logout, web).
func (t *Transport) ClearAuthCookies(w http.ResponseWriter) {
	t.setCookie(w, RefreshCookieName, "", RefreshCookiePath, true, time.Unix(0, 0))
	t.setCookie(w, CSRFCookieName, "", "/", false, time.Unix(0, 0))
}

func (t *Transport) setCookie(w http.ResponseWriter, name, value, path string, httpOnly bool, expires time.Time) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   t.cookieDomain,
		Expires:  expires,
		HttpOnly: httpOnly,
		Secure:   t.cookieSecure,
		SameSite: http.SameSiteLaxMode, // Lax so the OAuth redirect return works
	}
	if value == "" {
		c.MaxAge = -1 // delete
	}
	http.SetCookie(w, c)
}

// issueCSRF returns a signed double-submit token: nonce.hmac(nonce).
func (t *Transport) issueCSRF() string {
	nonce := randHex(16)
	return nonce + "." + t.csrfMAC(nonce)
}

// ValidateCSRF checks that the cookie and header values match and the token's
// HMAC is valid (so an attacker who can set a cookie can't forge a valid token).
func (t *Transport) ValidateCSRF(cookieVal, headerVal string) bool {
	if cookieVal == "" || headerVal == "" || !ConstantTimeEqual(cookieVal, headerVal) {
		return false
	}
	nonce, mac, ok := strings.Cut(cookieVal, ".")
	if !ok {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(t.csrfMAC(nonce)))
}

func (t *Transport) csrfMAC(nonce string) string {
	m := hmac.New(sha256.New, t.csrfKey)
	m.Write([]byte(nonce))
	return hex.EncodeToString(m.Sum(nil))
}
