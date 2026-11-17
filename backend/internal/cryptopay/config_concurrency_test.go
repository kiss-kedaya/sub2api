package cryptopay

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScanConcurrencyConfigRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value int
		want  int
	}{
		{"automatic", 0, 0}, {"negative", -5, 1}, {"serial", 1, 1},
		{"paid node", 32, 32}, {"maximum", 64, 64}, {"too large", 1000, 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := DefaultCryptoConfig()
			patchJSON, err := json.Marshal(map[string]any{"scan_concurrency": map[string]int{ChainBSC: tc.value}})
			require.NoError(t, err)
			var patch CryptoConfigPatch
			require.NoError(t, json.Unmarshal(patchJSON, &patch))
			cfg, err := NormalizeConfig(patch.Apply(base))
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.ScanConcurrency[ChainBSC])
			require.Empty(t, base.ScanConcurrency, "normalization must not mutate caller maps")
			require.Equal(t, tc.want, CryptoConfigPatch{}.Apply(cfg).ScanConcurrency[ChainBSC])
			store := &configCASStore{}
			require.NoError(t, SaveConfigIfUnchanged(t.Context(), store, cfg, nil))
			loaded, _, err := LoadConfigForUpdate(t.Context(), store)
			require.NoError(t, err)
			require.Equal(t, tc.want, loaded.ScanConcurrency[ChainBSC])
			s := &Service{config: loaded}
			snapshot := s.Config()
			snapshot.ScanConcurrency[ChainBSC] = 16
			require.Equal(t, tc.want, s.Config().ScanConcurrency[ChainBSC], "config readers cannot change live concurrency")
		})
	}
	cfg, err := NormalizeConfig(CryptoConfig{})
	require.NoError(t, err)
	require.NotNil(t, cfg.ScanConcurrency)
	require.Zero(t, cfg.ScanConcurrency[ChainBSC], "omitted settings keep automatic concurrency")
}

func TestScanConcurrencyFingerprintAndPatch(t *testing.T) {
	base := DefaultCryptoConfig()
	base.ScanConcurrency = map[string]int{ChainBSC: 32, ChainEthereum: 4}
	same := base
	same.ScanConcurrency = map[string]int{ChainEthereum: 4, ChainBSC: 32}
	require.Equal(t, configFingerprint(base), configFingerprint(same))
	changed := map[string]int{ChainBSC: 64}
	patched := CryptoConfigPatch{ScanConcurrency: &changed}.Apply(base)
	require.NotEqual(t, configFingerprint(base), configFingerprint(patched), "concurrency changes must trigger reloads")
	empty := map[string]int{}
	cleared, err := NormalizeConfig(CryptoConfigPatch{ScanConcurrency: &empty}.Apply(base))
	require.NoError(t, err)
	require.Zero(t, cleared.ScanConcurrency[ChainBSC], "empty patch map restores automatic concurrency")
}
