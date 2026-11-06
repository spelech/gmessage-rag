package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// GetSetting returns the stored value for key and whether the key exists.
// A missing key is not an error.
func (db *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	if db == nil || db.Pool == nil {
		return "", false, errors.New("database is not connected")
	}
	if key == "" {
		return "", false, errors.New("setting key cannot be empty")
	}

	var value string
	err := db.Pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, true, nil
}

// GetSettings returns all stored settings as a key/value map.
func (db *DB) GetSettings(ctx context.Context) (map[string]string, error) {
	if db == nil || db.Pool == nil {
		return nil, errors.New("database is not connected")
	}

	rows, err := db.Pool.Query(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		settings[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate settings: %w", err)
	}
	return settings, nil
}

// SetSetting upserts a single setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	if db == nil || db.Pool == nil {
		return errors.New("database is not connected")
	}
	if key == "" {
		return errors.New("setting key cannot be empty")
	}
	const upsertSQL = `
INSERT INTO app_settings (key, value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`
	if _, err := db.Pool.Exec(ctx, upsertSQL, key, value); err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// SetSettings upserts multiple settings atomically in a single transaction.
func (db *DB) SetSettings(ctx context.Context, values map[string]string) error {
	if db == nil || db.Pool == nil {
		return errors.New("database is not connected")
	}
	if len(values) == 0 {
		return nil
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin settings transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const upsertSQL = `
INSERT INTO app_settings (key, value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`

	for key, value := range values {
		if key == "" {
			return errors.New("setting key cannot be empty")
		}
		if _, err := tx.Exec(ctx, upsertSQL, key, value); err != nil {
			return fmt.Errorf("set setting %q: %w", key, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit settings transaction: %w", err)
	}
	return nil
}
