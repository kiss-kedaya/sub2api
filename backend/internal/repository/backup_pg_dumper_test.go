package repository

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSafeRestoreSQLReaderRejectsShellAndFileCommands(t *testing.T) {
	for _, input := range []string{
		`\! id` + "\n",
		`\shell id` + "\n",
		`\copy users to '/tmp/users.csv'` + "\n",
		`\include '/tmp/payload.sql'` + "\n",
	} {
		reader, done := safeRestoreSQLReader(strings.NewReader(input))
		_, readErr := io.ReadAll(reader)
		scanErr := <-done
		require.Error(t, readErr)
		require.Error(t, scanErr)
		require.Contains(t, scanErr.Error(), "not allowed")
	}
}

func TestSafeRestoreSQLReaderPreservesDumpControlAndCopyData(t *testing.T) {
	input := "COPY users FROM stdin;\n\\!\tuser@example.com\n\\.\n"
	reader, done := safeRestoreSQLReader(strings.NewReader(input))
	output, readErr := io.ReadAll(reader)
	require.NoError(t, readErr)
	require.NoError(t, <-done)
	require.Equal(t, input, string(output))
}
