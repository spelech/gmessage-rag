package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedossierCommand_Flags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"redossier"})
	require.NoError(t, err)
	assert.Equal(t, "redossier", cmd.Name())

	assert.NotNil(t, cmd.Flags().Lookup("conv"))
	assert.NotNil(t, cmd.Flags().Lookup("since"))
	assert.NotNil(t, cmd.Flags().Lookup("format"))
	assert.NotNil(t, cmd.Flags().Lookup("dry-run"))
	assert.NotNil(t, cmd.Flags().Lookup("db"))
}

func TestRedossierCommand_Validation(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	// Missing --conv
	redossierConv = ""
	err := runRedossier(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provide --conv")

	// Invalid --since date
	redossierConv = "test-conv"
	redossierSince = "invalid-date"
	defer func() {
		redossierConv = ""
		redossierSince = ""
	}()

	err = runRedossier(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --since date")
}
