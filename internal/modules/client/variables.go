// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/secretbox"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// Errors about secrets.
var (
	// ErrNoSecretKey means the installation has no secret key, so it can't
	// store or use secret variables.
	ErrNoSecretKey = errors.New("client: no secret key is configured")
	// ErrSecretKeyChanged means the workspace's secrets were stored with
	// another secret key than the one configured now.
	ErrSecretKeyChanged = errors.New("client: the secret key changed")
)

// What an environment may hold.
const (
	maxVariables     = 500
	maxVariableKey   = 200
	maxVariableValue = 64 << 10
)

// Variable is one of an environment's variables. A secret's Value is
// empty when it is read: secrets are write-only.
type Variable struct {
	Key    string
	Value  string
	Secret bool
}

// VariableInput is a variable to store. A secret with a nil Value keeps the
// value it has.
type VariableInput struct {
	Key    string
	Value  *string
	Secret bool
}

// Variables are an environment's variables, in order, and their version.
type Variables struct {
	EnvironmentID uuid.UUID
	Items         []Variable
	// Version is 0 until the variables are first stored.
	Version   int64
	UpdatedAt time.Time
}

// UseSecretKey sets the installation's key-encryption key, which secret
// variables need. Without one, environments hold plain variables only.
func (m *Module) UseSecretKey(box *secretbox.Box) { m.kek = box }

// environment finds an environment and checks the user's role in its
// project.
func (m *Module) environment(ctx context.Context, userID, environmentID uuid.UUID, n need) (workspaces.Environment, error) {
	env, role, err := m.access.GetEnvironment(ctx, userID, environmentID)
	if errors.Is(err, workspaces.ErrNotFound) {
		return workspaces.Environment{}, ErrNotFound
	}
	if err != nil {
		return workspaces.Environment{}, fmt.Errorf("check access: %w", err)
	}
	if n == edit && !role.CanEdit() {
		return workspaces.Environment{}, ErrForbidden
	}
	return env, nil
}

// GetVariables returns an environment's variables. Every member of the
// workspace may read them; secrets come back without their values.
func (m *Module) GetVariables(ctx context.Context, userID, environmentID uuid.UUID) (Variables, error) {
	if _, err := m.environment(ctx, userID, environmentID, read); err != nil {
		return Variables{}, err
	}
	vs, _, err := m.loadVariables(ctx, m.db, environmentID, false)
	return vs, err
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadVariables reads an environment's variables, and the sealed values of
// its secrets by key.
func (m *Module) loadVariables(ctx context.Context, q querier, environmentID uuid.UUID, lock bool) (Variables, map[string][]byte, error) {
	vs := Variables{EnvironmentID: environmentID, Items: []Variable{}}
	query := `SELECT version, updated_at FROM environment_variable_sets WHERE environment_id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	err := q.QueryRow(ctx, query, environmentID).Scan(&vs.Version, &vs.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return vs, map[string][]byte{}, nil
	}
	if err != nil {
		return Variables{}, nil, fmt.Errorf("get variables: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT key, coalesce(value, ''), secret_value FROM environment_variables WHERE environment_id = $1 ORDER BY position`, environmentID)
	if err != nil {
		return Variables{}, nil, fmt.Errorf("get variables: %w", err)
	}
	sealed := map[string][]byte{}
	for rows.Next() {
		var (
			v      Variable
			secret []byte
		)
		if err := rows.Scan(&v.Key, &v.Value, &secret); err != nil {
			rows.Close()
			return Variables{}, nil, fmt.Errorf("get variables: %w", err)
		}
		if secret != nil {
			v.Secret, sealed[v.Key] = true, secret
		}
		vs.Items = append(vs.Items, v)
	}
	if err := rows.Err(); err != nil {
		return Variables{}, nil, fmt.Errorf("get variables: %w", err)
	}
	return vs, sealed, nil
}

// PutVariables replaces an environment's variables. Owners, admins and
// editors may. A secret sent without a value keeps the one it has.
func (m *Module) PutVariables(ctx context.Context, userID, environmentID uuid.UUID, pre workspaces.Precondition, in []VariableInput) (Variables, error) {
	env, err := m.environment(ctx, userID, environmentID, edit)
	if err != nil {
		return Variables{}, err
	}
	if err := checkVariables(in); err != nil {
		return Variables{}, err
	}
	newSecret := false
	for _, v := range in {
		newSecret = newSecret || v.Secret && v.Value != nil
	}
	if newSecret && m.kek == nil {
		return Variables{}, ErrNoSecretKey
	}
	var out Variables
	err = pgx.BeginFunc(ctx, m.db, func(tx pgx.Tx) error {
		// The set's row serializes changes; the first change creates it.
		if _, err := tx.Exec(ctx, `INSERT INTO environment_variable_sets (environment_id, version) VALUES ($1, 0) ON CONFLICT DO NOTHING`, environmentID); err != nil {
			return err
		}
		current, sealed, err := m.loadVariables(ctx, tx, environmentID, true)
		if err != nil {
			return err
		}
		if !pre.Matches(current.Version) {
			return ErrVersionMismatch
		}
		var key *secretbox.Box
		if newSecret {
			if key, err = m.dataKey(ctx, tx, env.WorkspaceID); err != nil {
				return err
			}
		}
		var errs validate.Errors
		values := make([][]byte, len(in))
		for i, v := range in {
			switch {
			case !v.Secret:
			case v.Value != nil:
				values[i] = key.Seal([]byte(*v.Value), secretAD(environmentID, v.Key))
			case sealed[v.Key] != nil:
				values[i] = sealed[v.Key]
			default:
				errs.Add(fmt.Sprintf("/variables/%d/value", i), "required", "Give the new secret a value.")
			}
		}
		if err := errs.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM environment_variables WHERE environment_id = $1`, environmentID); err != nil {
			return err
		}
		rows := make([][]any, len(in))
		for i, v := range in {
			var plain *string
			if !v.Secret {
				plain = v.Value
				if plain == nil {
					plain = new(string)
				}
			}
			rows[i] = []any{environmentID, i, v.Key, plain, values[i]}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"environment_variables"}, []string{"environment_id", "position", "key", "value", "secret_value"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE environment_variable_sets SET version = version + 1, updated_by = $2, updated_at = now() WHERE environment_id = $1`, environmentID, userID); err != nil {
			return err
		}
		out, _, err = m.loadVariables(ctx, tx, environmentID, false)
		return err
	})
	if err != nil {
		var fields validate.Errors
		if errors.As(err, &fields) || errors.Is(err, ErrVersionMismatch) || errors.Is(err, ErrSecretKeyChanged) {
			return Variables{}, err
		}
		return Variables{}, fmt.Errorf("put variables: %w", err)
	}
	return out, nil
}

// checkVariables validates variables to store: keys that a {{name}} can
// name, once each, and values of at most 64 KiB.
func checkVariables(in []VariableInput) error {
	var errs validate.Errors
	if len(in) > maxVariables {
		errs.Add("/variables", "maxItems", fmt.Sprintf("Keep at most %d variables in an environment.", maxVariables))
		return errs.Err()
	}
	seen := make(map[string]bool, len(in))
	for i, v := range in {
		at := fmt.Sprintf("/variables/%d", i)
		switch {
		case v.Key == "":
			errs.Add(at+"/key", "required", "Name the variable, such as baseUrl.")
		case utf8.RuneCountInString(v.Key) > maxVariableKey:
			errs.Add(at+"/key", "maxLength", fmt.Sprintf("Use a name of at most %d characters.", maxVariableKey))
		case !validKey(v.Key):
			errs.Add(at+"/key", "pattern", "Use a name without spaces or braces that doesn't start with $, such as baseUrl.")
		case seen[v.Key]:
			errs.Add(at+"/key", "unique", "Another variable has this name. Give each variable its own.")
		}
		seen[v.Key] = true
		if v.Value != nil && utf8.RuneCountInString(*v.Value) > maxVariableValue {
			errs.Add(at+"/value", "maxLength", "Use a value of at most 65,536 characters.")
		}
		if v.Value != nil && strings.ContainsRune(*v.Value, 0) {
			errs.Add(at+"/value", "pattern", "Values can't contain NUL characters.")
		}
	}
	return errs.Err()
}

func validKey(key string) bool {
	if !utf8.ValidString(key) || strings.HasPrefix(key, "$") {
		return false
	}
	for _, r := range key {
		if r == '{' || r == '}' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// secretAD binds a secret's ciphertext to its environment and name, so it
// doesn't decrypt anywhere else.
func secretAD(environmentID uuid.UUID, key string) []byte {
	return []byte("environment-variable:" + environmentID.String() + "/" + key)
}

func workspaceKeyAD(workspaceID uuid.UUID) []byte {
	return []byte("workspace-key:" + workspaceID.String())
}

// dataKey returns the workspace's data key, creating it the first time.
func (m *Module) dataKey(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) (*secretbox.Box, error) {
	if m.kek == nil {
		return nil, ErrNoSecretKey
	}
	fresh := m.kek.Seal(secretbox.NewKey(), workspaceKeyAD(workspaceID))
	if _, err := tx.Exec(ctx, `INSERT INTO workspace_keys (workspace_id, wrapped_key, key_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		workspaceID, fresh, m.kek.ID); err != nil {
		return nil, err
	}
	return m.openDataKey(ctx, tx, workspaceID)
}

// openDataKey unwraps the workspace's data key.
func (m *Module) openDataKey(ctx context.Context, q querier, workspaceID uuid.UUID) (*secretbox.Box, error) {
	if m.kek == nil {
		return nil, ErrNoSecretKey
	}
	var (
		wrapped []byte
		keyID   string
	)
	err := q.QueryRow(ctx, `SELECT wrapped_key, key_id FROM workspace_keys WHERE workspace_id = $1`, workspaceID).Scan(&wrapped, &keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Secrets without the key that sealed them can't be opened.
		return nil, ErrSecretKeyChanged
	}
	if err != nil {
		return nil, err
	}
	if keyID != m.kek.ID {
		return nil, ErrSecretKeyChanged
	}
	raw, err := m.kek.Open(wrapped, workspaceKeyAD(workspaceID))
	if err != nil {
		return nil, ErrSecretKeyChanged
	}
	return secretbox.New(raw)
}

// environmentValues returns the values of an environment's variables,
// secrets decrypted, and the names of its secrets.
func (m *Module) environmentValues(ctx context.Context, env workspaces.Environment) (map[string]string, []string, error) {
	vs, sealed, err := m.loadVariables(ctx, m.db, env.ID, false)
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]string, len(vs.Items))
	var secrets []string
	var key *secretbox.Box
	for _, v := range vs.Items {
		if !v.Secret {
			values[v.Key] = v.Value
			continue
		}
		if key == nil {
			if key, err = m.openDataKey(ctx, m.db, env.WorkspaceID); err != nil {
				return nil, nil, err
			}
		}
		plain, err := key.Open(sealed[v.Key], secretAD(env.ID, v.Key))
		if err != nil {
			return nil, nil, ErrSecretKeyChanged
		}
		values[v.Key] = string(plain)
		secrets = append(secrets, v.Key)
	}
	return values, secrets, nil
}
