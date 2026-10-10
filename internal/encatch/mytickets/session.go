package mytickets

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	sessionPrefix = "my_tickets:session:"
	jtiPrefix     = "my_tickets:jti:"
)

var ErrNoSession = errors.New("no session")

// Store keeps sessions and used token IDs in Redis.
type Store struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewStore(rdb *redis.Client, ttl time.Duration) *Store {
	return &Store{rdb: rdb, ttl: ttl}
}

// UseJTI records a token ID; it returns false if the ID was already used.
func (s *Store) UseJTI(ctx context.Context, issuer, jti string, until time.Time) (bool, error) {
	ttl := time.Until(until)
	if ttl < time.Minute {
		ttl = time.Minute
	}
	return s.rdb.SetNX(ctx, jtiPrefix+IssuerKey(issuer)+":"+jti, 1, ttl).Result()
}

// Create stores a new session and returns its token.
func (s *Store) Create(ctx context.Context, sess Session) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if sess.CSRF, err = randomToken(); err != nil {
		return "", err
	}
	sess.CreatedAt = time.Now().UTC()
	b, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	// Fixed lifetime: the key is never refreshed, so sessions end ttl after sign-in.
	if err := s.rdb.Set(ctx, sessionPrefix+token, b, s.ttl).Err(); err != nil {
		return "", fmt.Errorf("storing session: %w", err)
	}
	return token, nil
}

// Update saves a changed session (e.g. a switched org) without extending its life.
func (s *Store) Update(ctx context.Context, token string, sess Session) error {
	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	// XX: only if it still exists; KeepTTL: the fixed lifetime from sign-in stays.
	return s.rdb.SetArgs(ctx, sessionPrefix+token, b, redis.SetArgs{Mode: "XX", KeepTTL: true}).Err()
}

// Get loads a session by token.
func (s *Store) Get(ctx context.Context, token string) (Session, error) {
	var sess Session
	if token == "" {
		return sess, ErrNoSession
	}
	b, err := s.rdb.Get(ctx, sessionPrefix+token).Bytes()
	if errors.Is(err, redis.Nil) {
		return sess, ErrNoSession
	}
	if err != nil {
		return sess, fmt.Errorf("loading session: %w", err)
	}
	if err := json.Unmarshal(b, &sess); err != nil {
		return sess, fmt.Errorf("decoding session: %w", err)
	}
	return sess, nil
}

// Delete ends a session.
func (s *Store) Delete(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.rdb.Del(ctx, sessionPrefix+token).Err()
}

// CSRFValid compares a submitted CSRF token with the session's in constant time.
func CSRFValid(sess Session, submitted string) bool {
	return sess.CSRF != "" && subtle.ConstantTimeCompare([]byte(sess.CSRF), []byte(submitted)) == 1
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
