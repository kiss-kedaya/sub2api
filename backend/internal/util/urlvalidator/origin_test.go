package urlvalidator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateHTTPSOriginHasStrictSecretSafeBoundary(t *testing.T) {
	for _, raw := range []string{"https://public.example", "https://public.example/", "https://public.example:8443", "https://8.8.8.8"} {
		require.NoError(t, ValidateHTTPSOrigin(raw), raw)
	}
	for _, raw := range []string{
		"", "http://public.example", "https://SECRET:SECRET@public.example", "https://public.example/path",
		"https://public.example/%2f", "https://public.example?key=SECRET", "https://public.example?", "https://public.example#",
		"https://public.example:", "https://public.example:0", "https://public.example:65536", " https://public.example",
		"https://localhost", "https://name.localhost.", "https://127.0.0.1", "https://[::1]", "https://10.0.0.1",
		"https://100.100.100.200", "https://[::ffff:127.0.0.1]", "https://[fe80::1%25eth0]", "https://host.internal",
	} {
		err := ValidateHTTPSOrigin(raw)
		require.Error(t, err, raw)
		require.NotContains(t, err.Error(), "SECRET")
		require.NotContains(t, err.Error(), "public.example")
	}
}
