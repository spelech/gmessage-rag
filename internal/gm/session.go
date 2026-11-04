package gm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// SessionFileMode is the POSIX file mode required for session token security (SEC-01 / AUTH-01).
const SessionFileMode = 0600

// LoadAuthData loads *libgm.AuthData from path if it exists; otherwise creates new AuthData.
func LoadAuthData(path string) (*libgm.AuthData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return libgm.NewAuthData(), nil
		}
		return nil, fmt.Errorf("failed to read session file %s: %w", path, err)
	}

	authData := &libgm.AuthData{}
	if err := json.Unmarshal(data, authData); err != nil {
		return nil, fmt.Errorf("failed to parse session json: %w", err)
	}

	return authData, nil
}

// SaveAuthData atomically writes *libgm.AuthData to path.tmp with POSIX mode 0600 and renames to path.
func SaveAuthData(path string, authData *libgm.AuthData) error {
	if authData == nil {
		return errors.New("cannot save nil auth data")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create auth directory %s: %w", dir, err)
	}

	jsonData, err := json.MarshalIndent(authData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth data: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, jsonData, SessionFileMode); err != nil {
		return fmt.Errorf("failed to write tmp session file %s: %w", tmpPath, err)
	}

	// Double check file permissions before rename
	if err := os.Chmod(tmpPath, SessionFileMode); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to enforce 0600 permissions on %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename %s to %s: %w", tmpPath, path, err)
	}

	return nil
}
