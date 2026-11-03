package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIngestCommand_Flags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, "ingest", cmd.Name())

	assert.NotNil(t, cmd.Flags().Lookup("conv"))
	assert.NotNil(t, cmd.Flags().Lookup("contact"))
	assert.NotNil(t, cmd.Flags().Lookup("limit"))
	assert.NotNil(t, cmd.Flags().Lookup("session"))
	assert.NotNil(t, cmd.Flags().Lookup("db"))
	assert.NotNil(t, cmd.Flags().Lookup("skip-llm"))
	assert.NotNil(t, cmd.Flags().Lookup("reconcile-only"))
}

func TestInspectCommand_Flags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"inspect"})
	require.NoError(t, err)
	assert.Equal(t, "inspect", cmd.Name())

	assert.NotNil(t, cmd.Flags().Lookup("limit"))
	assert.NotNil(t, cmd.Flags().Lookup("conv"))
	assert.NotNil(t, cmd.Flags().Lookup("session"))
}

func TestIngestCommand_ReconcileOnly(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("skipping test: DATABASE_URL not set")
	}

	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")
	t.Setenv("SESSION_PATH", sessionFile)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	ingestConvID = "non-existent-conv-test"
	ingestReconcileOnly = true
	defer func() {
		ingestConvID = ""
		ingestReconcileOnly = false
	}()

	err := runIngest(cmd, nil)
	assert.NoError(t, err)
}

func TestInspectCommand_NotLoggedIn(t *testing.T) {
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	inspectSession = sessionFile
	defer func() {
		inspectSession = ""
	}()

	err := runInspect(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not logged in / paired")
}

func TestIngestCommand_NotLoggedIn(t *testing.T) {
	tempDir := t.TempDir()
	sessionFile := filepath.Join(tempDir, "session.json")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	ingestSession = sessionFile
	ingestReconcileOnly = false
	defer func() {
		ingestSession = ""
	}()

	err := runIngest(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not logged in / paired")
}
