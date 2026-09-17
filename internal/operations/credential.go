package operations

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

var (
	ErrIdempotencyCredentialInvalid = errors.New("operation idempotency credential is invalid")
	ErrIdempotencyCredentialExpired = errors.New("operation idempotency credential expired")
)

const (
	minCredentialLifetimeSeconds = 60
	maxCredentialLifetimeSeconds = 7 * 24 * 60 * 60
	defaultCredentialLifetime    = int64(time.Hour / time.Second)
	operationKeyPrefix           = "opcred:"
)

// IdempotencyCredentialRequest fixes the command type and target scope before
// a server-signed operation key can be issued.
type IdempotencyCredentialRequest struct {
	OperationType   string `json:"type"`
	BaseID          string `json:"baseId,omitempty"`
	DocumentID      string `json:"documentId,omitempty"`
	LifetimeSeconds int    `json:"lifetimeSeconds,omitempty"`
}

// IdempotencyCredential is the one-time client representation. The durable
// operation stores OperationKey, never the signed credential itself.
type IdempotencyCredential struct {
	Credential    string `json:"credential"`
	OperationKey  string `json:"operationKey"`
	KeyID         string `json:"keyId"`
	OperationType string `json:"type"`
	BaseID        string `json:"baseId,omitempty"`
	DocumentID    string `json:"documentId,omitempty"`
	ExpiresAt     int64  `json:"expiresAt"`
}

type credentialClaims struct {
	KeyID         string `json:"kid"`
	CredentialID  string `json:"jti"`
	OperationType string `json:"type"`
	BaseID        string `json:"base,omitempty"`
	DocumentID    string `json:"doc,omitempty"`
	ExpiresAt     int64  `json:"exp"`
}

type signingKey struct {
	ID            string
	Secret        []byte
	State         string
	RetainedUntil sql.NullInt64
}

// ensureIdempotencySigningKey creates the persisted validation secret on first
// use. Restart does not rotate it or invalidate credentials already issued.
func (s *Service) ensureIdempotencySigningKey(ctx context.Context) error {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM operation_signing_keys WHERE state = 'active' LIMIT 1`).Scan(&id)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read operation signing key: %w", err)
	}
	keyID, secret, err := newSigningSecret()
	if err != nil {
		return err
	}
	_, err = s.db.ExecPriority(ctx, storage.ControlWrite, `INSERT INTO operation_signing_keys
		(id, secret, state, created_at) VALUES (?, ?, 'active', ?)`,
		keyID, hex.EncodeToString(secret), now())
	if err != nil {
		return fmt.Errorf("create operation signing key: %w", err)
	}
	return nil
}

func newSigningSecret() (string, []byte, error) {
	id, err := newID()
	if err != nil {
		return "", nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", nil, fmt.Errorf("generate operation signing secret: %w", err)
	}
	return id, secret, nil
}

func activeSigningKey(ctx context.Context, db *storage.DB) (signingKey, error) {
	return scanSigningKey(db.QueryRowContext(ctx, `SELECT id, secret, state, retained_until
		FROM operation_signing_keys WHERE state = 'active' LIMIT 1`))
}

func scanSigningKey(row *sql.Row) (signingKey, error) {
	var key signingKey
	var secretText string
	err := row.Scan(&key.ID, &secretText, &key.State, &key.RetainedUntil)
	if err != nil {
		return signingKey{}, err
	}
	secret, err := hex.DecodeString(secretText)
	if err != nil {
		return signingKey{}, fmt.Errorf("decode operation signing secret: %w", err)
	}
	key.Secret = secret
	return key, nil
}

// IssueIdempotencyCredential signs a bounded scope. The same credential maps
// to the same durable operation key on every retry.
func (s *Service) IssueIdempotencyCredential(
	ctx context.Context, request IdempotencyCredentialRequest,
) (IdempotencyCredential, error) {
	return s.issueIdempotencyCredential(ctx, request)
}

func (s *Service) issueIdempotencyCredential(
	ctx context.Context, request IdempotencyCredentialRequest,
) (IdempotencyCredential, error) {
	request.OperationType = strings.TrimSpace(request.OperationType)
	if request.OperationType == "" {
		return IdempotencyCredential{}, fmt.Errorf("%w: operation type is required", ErrIdempotencyCredentialInvalid)
	}
	lifetime := int64(request.LifetimeSeconds)
	if lifetime == 0 {
		lifetime = defaultCredentialLifetime
	}
	if lifetime < minCredentialLifetimeSeconds || lifetime > maxCredentialLifetimeSeconds {
		return IdempotencyCredential{}, fmt.Errorf("%w: credential lifetime %d outside %d-%d seconds",
			ErrIdempotencyCredentialInvalid, lifetime, minCredentialLifetimeSeconds, maxCredentialLifetimeSeconds)
	}
	if err := s.ensureIdempotencySigningKey(ctx); err != nil {
		return IdempotencyCredential{}, err
	}
	key, err := activeSigningKey(ctx, s.db)
	if err != nil {
		return IdempotencyCredential{}, fmt.Errorf("load active operation signing key: %w", err)
	}
	credentialID, err := newID()
	if err != nil {
		return IdempotencyCredential{}, err
	}
	claims := credentialClaims{
		KeyID: key.ID, CredentialID: credentialID,
		OperationType: request.OperationType, BaseID: request.BaseID,
		DocumentID: request.DocumentID, ExpiresAt: now() + lifetime*1000,
	}
	credential, err := signCredential(claims, key.Secret)
	if err != nil {
		return IdempotencyCredential{}, err
	}
	return IdempotencyCredential{
		Credential: credential, OperationKey: operationKeyPrefix + credentialID,
		KeyID: key.ID, OperationType: claims.OperationType,
		BaseID: claims.BaseID, DocumentID: claims.DocumentID,
		ExpiresAt: claims.ExpiresAt,
	}, nil
}

func signCredential(claims credentialClaims, secret []byte) (string, error) {
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode operation credential: %w", err)
	}
	claimsText := base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(claimsText))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "opcred." + claimsText + "." + signature, nil
}

// ValidateIdempotencyCredential verifies the signature, retained signing key,
// expiry, and scope. It returns the stable key to persist with the operation.
func (s *Service) ValidateIdempotencyCredential(
	ctx context.Context, credential string, request Request,
) (IdempotencyCredential, error) {
	credential = strings.TrimSpace(credential)
	claims, _, err := s.verifyCredential(ctx, credential)
	if err != nil {
		return IdempotencyCredential{}, err
	}
	if claims.OperationType != request.Type || claims.BaseID != request.BaseID ||
		claims.DocumentID != request.DocumentID {
		return IdempotencyCredential{}, fmt.Errorf("%w: credential scope does not match request", ErrIdempotencyCredentialInvalid)
	}
	return IdempotencyCredential{
		Credential: credential, OperationKey: operationKeyPrefix + claims.CredentialID,
		KeyID: claims.KeyID, OperationType: claims.OperationType,
		BaseID: claims.BaseID, DocumentID: claims.DocumentID,
		ExpiresAt: claims.ExpiresAt,
	}, nil
}

func (s *Service) verifyCredential(ctx context.Context, credential string) (credentialClaims, signingKey, error) {
	parts := strings.Split(credential, ".")
	if len(parts) != 3 || parts[0] != "opcred" || parts[1] == "" || parts[2] == "" {
		return credentialClaims{}, signingKey{}, fmt.Errorf("%w: malformed credential", ErrIdempotencyCredentialInvalid)
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return credentialClaims{}, signingKey{}, fmt.Errorf("%w: malformed credential payload", ErrIdempotencyCredentialInvalid)
	}
	var claims credentialClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil ||
		claims.KeyID == "" || claims.CredentialID == "" || claims.OperationType == "" {
		return credentialClaims{}, signingKey{}, fmt.Errorf("%w: malformed credential claims", ErrIdempotencyCredentialInvalid)
	}
	key, err := scanSigningKey(s.db.QueryRowContext(ctx,
		`SELECT id, secret, state, retained_until FROM operation_signing_keys WHERE id = ?`, claims.KeyID))
	if err == sql.ErrNoRows {
		return claims, key, fmt.Errorf("%w: signing key is unavailable", ErrIdempotencyCredentialInvalid)
	}
	if err != nil {
		return claims, key, fmt.Errorf("read operation signing key: %w", err)
	}
	if key.State == "revoked" {
		return claims, key, fmt.Errorf("%w: signing key was revoked", ErrIdempotencyCredentialInvalid)
	}
	mac := hmac.New(sha256.New, key.Secret)
	mac.Write([]byte(parts[1]))
	expected, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(mac.Sum(nil), expected) {
		return claims, key, fmt.Errorf("%w: signature mismatch", ErrIdempotencyCredentialInvalid)
	}
	if claims.ExpiresAt <= now() {
		return claims, key, ErrIdempotencyCredentialExpired
	}
	return claims, key, nil
}

// RotateIdempotencySigningKey makes a new active validation secret. Existing
// signing keys remain valid until explicit revocation or retention expiry.
func (s *Service) RotateIdempotencySigningKey(ctx context.Context, retention time.Duration) (string, error) {
	if retention < time.Hour {
		retention = 7 * 24 * time.Hour
	}
	keyID, secret, err := newSigningSecret()
	if err != nil {
		return "", err
	}
	retainUntil := now() + retention.Milliseconds()
	err = s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE operation_signing_keys
		SET state = 'retired', rotated_at = ?, retained_until = ?
		WHERE state = 'active'`, now(), retainUntil); err != nil {
			return fmt.Errorf("retire operation signing key: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_signing_keys
		(id, secret, state, created_at) VALUES (?, ?, 'active', ?)`,
			keyID, hex.EncodeToString(secret), now()); err != nil {
			return fmt.Errorf("create operation signing key: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return keyID, nil
}

// RevokeIdempotencySigningKey immediately rejects every credential signed by
// the key, including credentials whose signed expiry is still in the future.
func (s *Service) RevokeIdempotencySigningKey(ctx context.Context, keyID string) error {
	result, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE operation_signing_keys
		SET state = 'revoked', rotated_at = ?, retained_until = NULL
		WHERE id = ? AND state IN ('active','retired')`, now(), keyID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
