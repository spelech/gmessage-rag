package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadCookieInput_File(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "cookies.txt")
	err := os.WriteFile(tempFile, []byte("SID=123; HSID=456"), 0600)
	require.NoError(t, err)

	content, err := readCookieInput(tempFile)
	require.NoError(t, err)
	assert.Equal(t, "SID=123; HSID=456", content)
}

func TestReadCookieInput_Inline(t *testing.T) {
	content, err := readCookieInput("SID=123; HSID=456")
	require.NoError(t, err)
	assert.Equal(t, "SID=123; HSID=456", content)

	content2, err := readCookieInput(`{"SID":"123"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"SID":"123"}`, content2)
}
