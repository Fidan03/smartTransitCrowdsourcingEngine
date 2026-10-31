// Package auth provides operator accounts and session authentication.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"smart-transit/internal/clock"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUnauthenticated    = errors.New("not authenticated")
)

const (
	RoleOperator = "OPERATOR"
	RoleAdmin    = "ADMIN"
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// CanManageAlerts reports whether the role may acknowledge/resolve alerts.
func (u User) CanManageAlerts() bool { return u.Role == RoleOperator || u.Role == RoleAdmin }

type Service struct {
	pool       *pgxpool.Pool
	clock      clock.Clock
	log        *slog.Logger
	sessionTTL time.Duration
	dummyHash  []byte // compared when the email is unknown, to equalize timing
}

func NewService(pool *pgxpool.Pool, clk clock.Clock, log *slog.Logger, sessionTTL time.Duration) *Service {
	dummy, _ := bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcrypt.DefaultCost)
	return &Service{pool: pool, clock: clk, log: log, sessionTTL: sessionTTL, dummyHash: dummy}
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// EnsureUser creates the user if no account with that email exists.
func (s *Service) EnsureUser(ctx context.Context, email, password, role string) (created bool, err error) {
	email = normalizeEmail(email)
	if email == "" || len(password) < 12 {
		return false, errors.New("operator email required and password must be at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3)
		ON CONFLICT (email) DO NOTHING`, email, string(hash), role)
	return tag.RowsAffected() == 1, err
}

// Login verifies credentials and creates a session. It returns the raw token
// (to be set in a cookie); only its SHA-256 hash is stored.
func (s *Service) Login(ctx context.Context, email, password string) (token string, user User, expires time.Time, err error) {
	var hash string
	err = s.pool.QueryRow(ctx, `SELECT id::text, email, role, password_hash FROM users WHERE email = $1`,
		normalizeEmail(email)).Scan(&user.ID, &user.Email, &user.Role, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return "", User{}, time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, time.Time{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.audit(ctx, &user.ID, "auth.login_failed", nil)
		return "", User{}, time.Time{}, ErrInvalidCredentials
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", User{}, time.Time{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	now := s.clock.Now()
	expires = now.Add(s.sessionTTL)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4)`,
		hashToken(token), user.ID, now, expires); err != nil {
		return "", User{}, time.Time{}, err
	}
	// Opportunistic cleanup of expired sessions.
	_, _ = s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now)
	s.audit(ctx, &user.ID, "auth.login", nil)
	return token, user, expires, nil
}

// Authenticate resolves a session token to its user.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrUnauthenticated
	}
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.email, u.role
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, hashToken(token), s.clock.Now(),
	).Scan(&u.ID, &u.Email, &u.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	return u, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

func (s *Service) audit(ctx context.Context, userID *string, action string, meta map[string]string) {
	if meta == nil {
		meta = map[string]string{}
	}
	b, _ := json.Marshal(meta)
	if _, err := s.pool.Exec(ctx, `INSERT INTO audit_logs (user_id, action, created_at, metadata) VALUES ($1, $2, $3, $4)`,
		userID, action, s.clock.Now(), b); err != nil {
		s.log.Error("audit log write failed", "action", action, "err", err)
	}
}
