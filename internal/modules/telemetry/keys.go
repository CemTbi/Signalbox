// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/token"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// IngestKeyPrefix starts every ingest key, so secret scanners and people
// can tell one apart from other secrets.
const IngestKeyPrefix = "sbx_ing_"

const (
	maxIngestKeys    = 50
	maxIngestKeyName = 100
	// keyCacheTTL is how long a process trusts a key it looked up, so a
	// busy exporter doesn't cost a query per request. Revoking a key takes
	// at most this long to reach other processes.
	keyCacheTTL = 10 * time.Second
	// usedEvery is how often a key's last use is written.
	usedEvery = time.Minute
)

var (
	// ErrNotFound means the environment or key doesn't exist, or the user
	// can't see it.
	ErrNotFound = errors.New("telemetry: not found")
	// ErrForbidden means the user's role doesn't allow managing keys.
	ErrForbidden = errors.New("telemetry: not allowed")
	// ErrTooManyKeys means the environment has as many keys as it may.
	ErrTooManyKeys = errors.New("telemetry: too many ingest keys")
	// ErrUnknownKey means a key is malformed, revoked or never existed.
	ErrUnknownKey = errors.New("telemetry: unknown ingest key")
)

// IngestKey is a key that sends spans to one environment, without its
// secret.
type IngestKey struct {
	ID            uuid.UUID
	EnvironmentID uuid.UUID
	Name          string
	// Hint is the key's last four characters, to tell keys apart.
	Hint string
	// CreatedBy is uuid.Nil once its creator's account is gone.
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// NewIngestKey is a key that was just created, with its secret, which
// isn't stored and can't be retrieved later.
type NewIngestKey struct {
	IngestKey
	Key string
}

// Sender is who sends spans: the environment a key writes to.
type Sender struct {
	KeyID         uuid.UUID
	EnvironmentID uuid.UUID
}

// Access tells the module what a user may do in an environment;
// *workspaces.Service is one.
type Access interface {
	GetEnvironment(ctx context.Context, userID, environmentID uuid.UUID) (workspaces.Environment, workspaces.Role, error)
}

// Keys keeps ingest keys and checks the keys that requests carry.
type Keys struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	access Access
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cachedKey // by the key's hash
	used  map[uuid.UUID]time.Time
}

type cachedKey struct {
	sender  Sender
	expires time.Time
}

// NewKeys returns the key store.
func NewKeys(db *pgxpool.Pool, logger *slog.Logger, access Access) *Keys {
	return &Keys{db: db, logger: logger, access: access, now: time.Now, cache: map[string]cachedKey{}, used: map[uuid.UUID]time.Time{}}
}

// authorize checks that the user can see the environment and, to change
// its keys, is an owner or admin.
func (k *Keys) authorize(ctx context.Context, userID, environmentID uuid.UUID, manage bool) error {
	_, role, err := k.access.GetEnvironment(ctx, userID, environmentID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if manage && !role.CanDelete() {
		return ErrForbidden
	}
	return nil
}

// Create makes a key that sends spans to the environment. Owners and
// admins may.
func (k *Keys) Create(ctx context.Context, userID, environmentID uuid.UUID, name string) (*NewIngestKey, error) {
	if err := k.authorize(ctx, userID, environmentID, true); err != nil {
		return nil, err
	}
	var errs validate.Errors
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		errs.Add("/name", "required", "Name the key after what sends with it, such as checkout-service.")
	case utf8.RuneCountInString(name) > maxIngestKeyName:
		errs.Add("/name", "maxLength", fmt.Sprintf("Use at most %d characters.", maxIngestKeyName))
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}

	secret := IngestKeyPrefix + token.Random()
	key := &NewIngestKey{Key: secret, IngestKey: IngestKey{
		ID: id.New(), EnvironmentID: environmentID, Name: name, Hint: secret[len(secret)-4:], CreatedBy: userID,
	}}
	err := pgx.BeginFunc(ctx, k.db, func(tx pgx.Tx) error {
		// Serializes an environment's key creations, so the count holds.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM environments WHERE id = $1 FOR UPDATE`, environmentID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ingest_keys WHERE environment_id = $1`, environmentID).Scan(&n); err != nil {
			return err
		}
		if n >= maxIngestKeys {
			return ErrTooManyKeys
		}
		return tx.QueryRow(ctx, `
			INSERT INTO ingest_keys (id, environment_id, name, key_hash, hint, created_by)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
			key.ID, environmentID, name, token.Hash(secret), key.Hint, userID,
		).Scan(&key.CreatedAt)
	})
	if errors.Is(err, ErrTooManyKeys) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("create an ingest key: %w", err)
	}
	k.logger.InfoContext(ctx, "created ingest key", id.LogAttr("key_id", id.IngestKey, key.ID),
		id.LogAttr("environment_id", id.Environment, environmentID), id.LogAttr("user_id", id.User, userID))
	return key, nil
}

const keyColumns = `id, environment_id, name, hint, coalesce(created_by, '00000000-0000-0000-0000-000000000000'), created_at, last_used_at`

func readKey(row pgx.Row) (IngestKey, error) {
	var key IngestKey
	err := row.Scan(&key.ID, &key.EnvironmentID, &key.Name, &key.Hint, &key.CreatedBy, &key.CreatedAt, &key.LastUsedAt)
	return key, err
}

// List lists the environment's keys, newest first. Every member of the
// project may see them; none shows its secret.
func (k *Keys) List(ctx context.Context, userID, environmentID uuid.UUID) ([]IngestKey, error) {
	if err := k.authorize(ctx, userID, environmentID, false); err != nil {
		return nil, err
	}
	rows, err := k.db.Query(ctx, `SELECT `+keyColumns+` FROM ingest_keys WHERE environment_id = $1 ORDER BY id DESC`, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	keys, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (IngestKey, error) { return readKey(row) })
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	return keys, nil
}

// Revoke deletes a key. Spans sent with it are refused at once in this
// process, and within 10 seconds in others. Owners and admins may.
func (k *Keys) Revoke(ctx context.Context, userID, keyID uuid.UUID) error {
	var environmentID uuid.UUID
	err := k.db.QueryRow(ctx, `SELECT environment_id FROM ingest_keys WHERE id = $1`, keyID).Scan(&environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find an ingest key: %w", err)
	}
	if err := k.authorize(ctx, userID, environmentID, true); err != nil {
		return err
	}
	if _, err := k.db.Exec(ctx, `DELETE FROM ingest_keys WHERE id = $1`, keyID); err != nil {
		return fmt.Errorf("revoke an ingest key: %w", err)
	}
	k.mu.Lock()
	for hash, c := range k.cache {
		if c.sender.KeyID == keyID {
			delete(k.cache, hash)
		}
	}
	delete(k.used, keyID)
	k.mu.Unlock()
	k.logger.InfoContext(ctx, "revoked ingest key", id.LogAttr("key_id", id.IngestKey, keyID), id.LogAttr("user_id", id.User, userID))
	return nil
}

// Authenticate finds who sends with a key. Keys that can't exist never
// reach the database.
func (k *Keys) Authenticate(ctx context.Context, key string) (Sender, error) {
	rest, ok := strings.CutPrefix(key, IngestKeyPrefix)
	if !ok || !token.WellFormed(rest) {
		return Sender{}, ErrUnknownKey
	}
	hash := string(token.Hash(key))
	now := k.now()
	k.mu.Lock()
	c, ok := k.cache[hash]
	k.mu.Unlock()
	if ok && now.Before(c.expires) {
		k.markUsed(ctx, c.sender.KeyID, now)
		return c.sender, nil
	}

	var s Sender
	err := k.db.QueryRow(ctx, `SELECT id, environment_id FROM ingest_keys WHERE key_hash = $1`, []byte(hash)).Scan(&s.KeyID, &s.EnvironmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		k.mu.Lock()
		delete(k.cache, hash)
		k.mu.Unlock()
		return Sender{}, ErrUnknownKey
	}
	if err != nil {
		return Sender{}, fmt.Errorf("look up an ingest key: %w", err)
	}
	k.mu.Lock()
	if len(k.cache) >= 10_000 {
		clear(k.cache) // far more keys than any installation uses at once
	}
	k.cache[hash] = cachedKey{sender: s, expires: now.Add(keyCacheTTL)}
	k.mu.Unlock()
	k.markUsed(ctx, s.KeyID, now)
	return s, nil
}

// markUsed records a key's use at most once a minute.
func (k *Keys) markUsed(ctx context.Context, keyID uuid.UUID, now time.Time) {
	k.mu.Lock()
	due := now.Sub(k.used[keyID]) >= usedEvery
	if due {
		k.used[keyID] = now
	}
	k.mu.Unlock()
	if !due {
		return
	}
	if _, err := k.db.Exec(ctx, `UPDATE ingest_keys SET last_used_at = now() WHERE id = $1`, keyID); err != nil && ctx.Err() == nil {
		k.logger.WarnContext(ctx, "couldn't record an ingest key's use", "error", err, id.LogAttr("key_id", id.IngestKey, keyID))
	}
}
