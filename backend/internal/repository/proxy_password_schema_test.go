package repository

import (
	"strings"
	"testing"

	entproxy "github.com/Wei-Shaw/sub2api/ent/proxy"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/stretchr/testify/require"
)

// The generated validators are what reject over-length proxy rows before the
// database sees them. They must tolerate the ciphertext the managed import path
// writes and still count name bytes, not runes.
func TestGeneratedProxyValidatorsAcceptManagedImportValues(t *testing.T) {
	// "v2:" + base64(12-byte nonce + 64-byte ciphertext + 16-byte tag) = 127 bytes.
	ciphertext := "v2:" + strings.Repeat("A", 124)
	require.Len(t, ciphertext, 127)
	require.NotNil(t, entproxy.PasswordValidator)
	require.NoError(t, entproxy.PasswordValidator(ciphertext))
	// The proxy column is TEXT and long upstream passwords expand past the old
	// 255-byte limit; only the entity's defensive bound may reject absurd input.
	require.NoError(t, entproxy.PasswordValidator(strings.Repeat("A", 256)))
	require.Error(t, entproxy.PasswordValidator(strings.Repeat("A", 4097)))

	// proxies.name stays at 100 bytes; 33 CJK runes plus one ASCII byte is a
	// valid boundary and anything longer is rejected.
	name := strings.Repeat("中", 33) + "a"
	require.Len(t, name, 100)
	require.NoError(t, entproxy.NameValidator(name))
	require.Error(t, entproxy.NameValidator(name+"a"))
}
