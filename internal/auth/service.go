package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"backend/internal/audit"
	"backend/internal/repository"
)

// Login/refresh errors surfaced to the transport layer.
var (
	ErrRefreshInvalid      = errors.New("refresh token invalid or expired")
	ErrRefreshReuse        = errors.New("refresh token reuse detected; session revoked")
	ErrRefreshInProgress   = errors.New("concurrent refresh in progress; retry")
	ErrAccountNotActive    = errors.New("account is not active")
	ErrGoogleNotConfigured = errors.New("google login is not configured")
	ErrInvalidState        = errors.New("invalid or expired login state")
)

// ClientType selects token-delivery style (cookie vs body).
type ClientType string

const (
	ClientWeb    ClientType = "web"
	ClientNative ClientType = "native"
)

// LoginStatus models the login pipeline outcome — the seam for future 2FA.
type LoginStatus string

const (
	LoginComplete    LoginStatus = "complete"
	LoginMFARequired LoginStatus = "mfa_required" // reserved for future TOTP/SMS
)

// DeviceInfo describes the client device for a session.
type DeviceInfo struct {
	UserAgent string
	IP        string
	Label     string
}

// Tokens is the set of credentials issued for a session. RefreshToken is the
// raw value and is returned to the caller exactly once.
type Tokens struct {
	AccessToken   string
	AccessExpiry  time.Time
	RefreshToken  string
	RefreshExpiry time.Time
	SessionID     string
	Roles         []string
}

// LoginResult is what the login pipeline returns. When Status is
// LoginMFARequired, Tokens is empty and a challenge step is required (future).
type LoginResult struct {
	Status LoginStatus
	User   repository.User
	Tokens Tokens
}

// Service orchestrates authentication: provisioning, session issuance, refresh
// rotation, and logout. It depends only on repositories + the token service +
// the status cache, mirroring the codebase's dependency-injection style.
type Service struct {
	users               *repository.UserRepository
	identities          *repository.IdentityRepository
	sessions            *repository.SessionRepository
	roles               *repository.RoleRepository
	tokens              *TokenService
	cache               StatusCache
	audit               *audit.Recorder
	flows               *repository.OAuthFlowRepository
	google              *GoogleProvider // nil if Google is not configured
	refreshTTL          time.Duration
	bootstrapAdminEmail string
	log                 *slog.Logger
}

// Deps bundles the Service's dependencies.
type Deps struct {
	Users               *repository.UserRepository
	Identities          *repository.IdentityRepository
	Sessions            *repository.SessionRepository
	Roles               *repository.RoleRepository
	Tokens              *TokenService
	Cache               StatusCache
	Audit               *audit.Recorder
	Flows               *repository.OAuthFlowRepository
	Google              *GoogleProvider
	RefreshTTL          time.Duration
	BootstrapAdminEmail string
	Log                 *slog.Logger
}

// NewService builds the auth Service.
func NewService(d Deps) *Service {
	return &Service{
		users:               d.Users,
		identities:          d.Identities,
		sessions:            d.Sessions,
		roles:               d.Roles,
		tokens:              d.Tokens,
		cache:               d.Cache,
		audit:               d.Audit,
		flows:               d.Flows,
		google:              d.Google,
		refreshTTL:          d.RefreshTTL,
		bootstrapAdminEmail: d.BootstrapAdminEmail,
		log:                 d.Log,
	}
}

// GoogleEnabled reports whether Google login is configured.
func (s *Service) GoogleEnabled() bool { return s.google != nil }

// IssueSession creates a new device session and mints an access + refresh token
// pair. Roles are read fresh at issue time. Used by the login pipeline (and,
// later, by the MFA-verify step).
func (s *Service) IssueSession(ctx context.Context, user repository.User, dev DeviceInfo) (Tokens, error) {
	roles, err := s.roles.RolesForUser(ctx, user.ID)
	if err != nil {
		return Tokens{}, err
	}

	rawRefresh, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	expiry := time.Now().Add(s.refreshTTL)

	sessionID, err := s.sessions.Create(ctx, repository.CreateSessionParams{
		UserID:           user.ID,
		RefreshTokenHash: refreshHash,
		UserAgent:        dev.UserAgent,
		IP:               dev.IP,
		DeviceLabel:      dev.Label,
		ExpiresAt:        expiry,
	})
	if err != nil {
		return Tokens{}, err
	}

	access, accessExp, err := s.tokens.IssueAccessToken(user.ID, sessionID, roles)
	if err != nil {
		return Tokens{}, err
	}

	return Tokens{
		AccessToken:   access,
		AccessExpiry:  accessExp,
		RefreshToken:  rawRefresh,
		RefreshExpiry: expiry,
		SessionID:     sessionID,
		Roles:         roles,
	}, nil
}

// StartGoogleLogin generates state + PKCE, persists the flow, and returns the
// Google consent URL the client should be redirected to.
func (s *Service) StartGoogleLogin(ctx context.Context, ct ClientType, redirectURI string) (string, error) {
	if s.google == nil {
		return "", ErrGoogleNotConfigured
	}
	state := randHex(16)
	verifier, challenge := GeneratePKCE()

	err := s.flows.Create(ctx, repository.OAuthFlow{
		State:        state,
		Provider:     "google",
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		ClientType:   string(ct),
	}, time.Now().Add(10*time.Minute))
	if err != nil {
		return "", err
	}
	return s.google.AuthCodeURL(state, challenge), nil
}

// HandleGoogleCallback consumes the login flow, exchanges the code, and runs the
// login pipeline. Returns the result plus the client type recorded at /start
// (so the caller delivers tokens in the right style) and the success redirect.
func (s *Service) HandleGoogleCallback(ctx context.Context, state, code string, dev DeviceInfo, meta ReqMeta) (LoginResult, ClientType, string, error) {
	if s.google == nil {
		return LoginResult{}, "", "", ErrGoogleNotConfigured
	}

	flow, err := s.flows.Consume(ctx, state)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return LoginResult{}, "", "", ErrInvalidState
		}
		return LoginResult{}, "", "", err
	}
	ct := ClientType(flow.ClientType)
	if ct != ClientWeb && ct != ClientNative {
		ct = ClientWeb
	}

	ext, err := s.google.Exchange(ctx, code, flow.CodeVerifier)
	if err != nil {
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventLoginFailure,
			IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
			Detail: map[string]any{"provider": "google", "reason": "exchange_failed"},
		})
		return LoginResult{}, "", "", err
	}

	result, err := s.completeLogin(ctx, ext, dev, meta)
	if err != nil {
		return LoginResult{}, "", "", err
	}
	return result, ct, flow.RedirectURI, nil
}

// LoginWithGoogleIDToken verifies a Google ID token from a native mobile client
// and runs the login pipeline, returning a session. No redirect/cookies — the
// caller delivers the tokens in the response body (native client type).
func (s *Service) LoginWithGoogleIDToken(ctx context.Context, idToken string, dev DeviceInfo, meta ReqMeta) (LoginResult, error) {
	if s.google == nil {
		return LoginResult{}, ErrGoogleNotConfigured
	}
	ext, err := s.google.VerifyIDToken(ctx, idToken)
	if err != nil {
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventLoginFailure,
			IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
			Detail: map[string]any{"provider": "google", "reason": "invalid_id_token"},
		})
		return LoginResult{}, err
	}
	return s.completeLogin(ctx, ext, dev, meta)
}

// completeLogin provisions the user from an external identity, enforces account
// status, applies the (future) MFA gate, and issues a session.
func (s *Service) completeLogin(ctx context.Context, ext ExternalIdentity, dev DeviceInfo, meta ReqMeta) (LoginResult, error) {
	user, _, err := s.identities.FindOrCreateUser(ctx, repository.ProvisionParams{
		Provider:        ext.Provider,
		ProviderSubject: ext.Subject,
		Email:           ext.Email,
		Name:            ext.Name,
		EmailVerified:   ext.EmailVerified,
		Data:            ext.Raw,
	})
	if err != nil {
		return LoginResult{}, err
	}

	// Seed the first administrator: grant 'admin' to the configured bootstrap
	// email on login (best-effort, idempotent).
	if s.bootstrapAdminEmail != "" && strings.EqualFold(user.Email, s.bootstrapAdminEmail) {
		if e := s.roles.AssignRole(ctx, user.ID, "admin", ""); e != nil {
			s.log.Error("bootstrap admin grant failed", "user_id", user.ID, "error", e)
		}
	}

	if user.Status != "active" {
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventLoginFailure,
			UserID:    &user.ID,
			IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
			Detail: map[string]any{"reason": "account_" + user.Status},
		})
		return LoginResult{}, ErrAccountNotActive
	}

	// 2FA seam: when the user has a confirmed second factor, return
	// LoginMFARequired with a challenge token instead of a session. Not wired yet.

	tokens, err := s.IssueSession(ctx, user, dev)
	if err != nil {
		return LoginResult{}, err
	}

	s.audit.Record(ctx, repository.AuditEvent{
		EventType: audit.EventLoginSuccess,
		UserID:    &user.ID, SessionID: &tokens.SessionID,
		IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		Detail: map[string]any{"provider": ext.Provider},
	})

	return LoginResult{Status: LoginComplete, User: user, Tokens: tokens}, nil
}

// StatusResolver returns a resolver for the status cache, backed by the session
// repository. Wired into NewMemStatusCache at startup.
func (s *Service) StatusResolver() StatusResolver {
	return func(ctx context.Context, _ /*userID*/, sessionID string) (AccountStatus, error) {
		userStatus, revoked, err := s.sessions.Status(ctx, sessionID)
		if err != nil {
			return AccountStatus{}, err
		}
		return AccountStatus{UserStatus: userStatus, SessionRevoked: revoked}, nil
	}
}
