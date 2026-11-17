package cryptopay

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func fingerprintForTest(t *testing.T, cfg CryptoConfig) string {
	t.Helper()
	fingerprint, err := configFingerprint(cfg)
	require.NoError(t, err)
	return fingerprint
}

// Equal configs must not restart workers just because map iteration differs.
// All persisted fields, including crediting and sweep settings, affect reloads.
func TestConfigFingerprintCoversEveryExportedField(t *testing.T) {
	// No exclusions: CryptoConfig has no timestamps, nonces or display-only fields.
	excluded := map[string]string{}
	base := DefaultCryptoConfig()
	configType := reflect.TypeOf(base)
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)
		if !field.IsExported() {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			changed := DefaultCryptoConfig()
			mutateFingerprintField(t, reflect.ValueOf(&changed).Elem().FieldByIndex(field.Index))
			if reason, ok := excluded[field.Name]; ok {
				require.NotEmpty(t, reason)
				require.True(t, fingerprintForTest(t, base) == fingerprintForTest(t, changed), reason)
				return
			}
			require.True(t, fingerprintForTest(t, base) != fingerprintForTest(t, changed), "field must participate in reload detection")
		})
	}
}

func mutateFingerprintField(t *testing.T, value reflect.Value) {
	t.Helper()
	switch value.Kind() {
	case reflect.Bool:
		value.SetBool(!value.Bool())
	case reflect.String:
		value.SetString(value.String() + "changed")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(value.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(value.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		value.SetFloat(value.Float() + 1)
	case reflect.Slice:
		value.Set(reflect.Append(value, reflect.Zero(value.Type().Elem())))
	case reflect.Map:
		if value.IsNil() {
			value.Set(reflect.MakeMap(value.Type()))
		}
		entry := reflect.New(value.Type().Elem()).Elem()
		mutateFingerprintField(t, entry)
		value.SetMapIndex(reflect.ValueOf("changed").Convert(value.Type().Key()), entry)
	case reflect.Struct:
		mutateFingerprintField(t, value.Field(0))
	default:
		t.Fatalf("add a fingerprint mutation for %s", value.Type())
	}
}

func TestConfigFingerprintNormalizationIsIdempotent(t *testing.T) {
	for _, cfg := range []CryptoConfig{
		{},
		DefaultCryptoConfig(),
		{Chains: []string{ChainBSC, ChainBSC}, RPCThrottleMs: 25,
			RPCLimits:  map[string]RPCEndpointLimits{" https://rpc.example.test ": {MaxBlocks: 100}},
			NativeScan: " BLOCKS ", NativePriceURL: " https://price.example.test ",
			ScanRange: map[string]int{ChainBSC: -1}, ScanConcurrency: map[string]int{ChainBSC: 1000},
			ExplorerAPIURL: map[string]string{ChainBSC: " https://explorer.example.test/api/ "}},
	} {
		first, err := NormalizeConfig(cfg)
		require.NoError(t, err)
		raw, err := json.Marshal(first)
		require.NoError(t, err)
		var readBack CryptoConfig
		require.NoError(t, json.Unmarshal(raw, &readBack))
		second, err := NormalizeConfig(readBack)
		require.NoError(t, err)
		require.True(t, fingerprintForTest(t, first) == fingerprintForTest(t, second), "normalizing a stored snapshot again must not trigger reload")
	}
}

func TestConfigFingerprintRejectsNonSerializableValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg := DefaultCryptoConfig()
		cfg.CreditMultiplier = value
		fingerprint, err := configFingerprint(cfg)
		require.Error(t, err)
		require.Empty(t, fingerprint, "a serialization error must not look like an applied config")
	}
}

func TestConfigFingerprintTracksReloadRelevantFields(t *testing.T) {
	base := CryptoConfig{
		Enabled:       true,
		Chains:        []string{"bsc", "ethereum"},
		RPC:           map[string]string{"bsc": "https://a", "ethereum": "https://b"},
		ScanRange:     map[string]int{"bsc": 500, "ethereum": 2000},
		Confirmations: map[string]int{"bsc": 3, "ethereum": 64},
	}
	same := base
	same.RPC = map[string]string{"bsc": "https://a", "ethereum": "https://b"}
	same.ScanRange = map[string]int{"bsc": 500, "ethereum": 2000}
	same.Confirmations = map[string]int{"bsc": 3, "ethereum": 64}

	require.Equal(t, fingerprintForTest(t, base), fingerprintForTest(t, same),
		"an equal config must not look changed")

	t.Run("rpc endpoint changed", func(t *testing.T) {
		mutated := base
		mutated.RPC = map[string]string{"bsc": "https://new", "ethereum": "https://b"}
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("rpc limits changed", func(t *testing.T) {
		mutated := base
		mutated.RPCLimits = map[string]RPCEndpointLimits{"https://a": {MaxBlocks: 100, MaxAddresses: 20}}
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("rpc throttle changed", func(t *testing.T) {
		mutated := base
		mutated.RPCThrottleMs = 250
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("scan range changed", func(t *testing.T) {
		mutated := base
		mutated.ScanRange = map[string]int{"bsc": 250, "ethereum": 2000}
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("confirmations changed", func(t *testing.T) {
		mutated := base
		mutated.Confirmations = map[string]int{"bsc": 5, "ethereum": 64}
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("chain removed", func(t *testing.T) {
		mutated := base
		mutated.Chains = []string{"bsc"}
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("enabled toggled", func(t *testing.T) {
		mutated := base
		mutated.Enabled = false
		require.NotEqual(t, fingerprintForTest(t, base), fingerprintForTest(t, mutated))
	})

	t.Run("map order does not matter", func(t *testing.T) {
		a := base
		a.RPC = map[string]string{"bsc": "https://a", "ethereum": "https://b"}
		b := base
		b.RPC = map[string]string{"ethereum": "https://b", "bsc": "https://a"}
		require.Equal(t, fingerprintForTest(t, a), fingerprintForTest(t, b),
			"map iteration order must not cause a spurious reload")
	})
}
