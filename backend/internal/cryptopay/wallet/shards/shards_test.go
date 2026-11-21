package shards

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func clearShares(shares []Share) {
	for i := range shares {
		clear(shares[i].Data)
	}
}

func TestSplitCombineRecoversEveryPairInAnyOrder(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 32)
	shares, err := Split(secret, DefaultThreshold, DefaultTotal)
	require.NoError(t, err)
	require.Len(t, shares, DefaultTotal)
	texts := []string{shares[0].String(), shares[1].String(), shares[2].String()}
	defer clearShares(shares)

	pairs := [][2]int{{0, 1}, {0, 2}, {1, 2}, {1, 0}, {2, 0}, {2, 1}}
	for _, pair := range pairs {
		recovered, err := ParseAndCombine([]string{texts[pair[0]], texts[pair[1]]})
		require.NoError(t, err)
		require.Equal(t, secret, recovered)
		clear(recovered)
	}

	// Three shares are also consistent, but only two are ever required.
	recovered, err := ParseAndCombine(texts)
	require.NoError(t, err)
	require.Equal(t, secret, recovered)
	// A reconstructed key must match the published commitment.
	require.Equal(t, Commitment(secret), Commitment(recovered))
	clear(recovered)
}

func TestSingleShareCannotRecover(t *testing.T) {
	secret := bytes.Repeat([]byte{0x11}, 32)
	shares, err := Split(secret, DefaultThreshold, DefaultTotal)
	require.NoError(t, err)
	text := shares[0].String()
	clearShares(shares)

	_, err = ParseAndCombine([]string{text})
	require.ErrorIs(t, err, ErrThreshold)
}

func TestDuplicateAndMixedSharesRejected(t *testing.T) {
	secret := bytes.Repeat([]byte{0x22}, 32)
	first, err := Split(secret, DefaultThreshold, DefaultTotal)
	require.NoError(t, err)
	second, err := Split(secret, DefaultThreshold, DefaultTotal)
	require.NoError(t, err)
	defer clearShares(first)
	defer clearShares(second)

	_, err = Combine([]Share{first[0], first[0]})
	require.ErrorIs(t, err, ErrDuplicate)

	// Same parameters but from another split: Shamir cannot detect this, so
	// the caller must always compare the recovered key with the commitment.
	mixed, err := Combine([]Share{first[0], second[1]})
	require.NoError(t, err)
	require.NotEqual(t, Commitment(secret), Commitment(mixed))
	clear(mixed)
}

func TestChecksumDetectsTypo(t *testing.T) {
	secret := bytes.Repeat([]byte{0x33}, 32)
	shares, err := Split(secret, DefaultThreshold, DefaultTotal)
	require.NoError(t, err)
	text := shares[0].String()
	clearShares(shares)

	// Flip one base32 character in the middle, not in the prefix or a dash.
	index := strings.Index(text, "-")
	require.Greater(t, index, 0)
	position := index + 8
	replacement := byte('A')
	if text[position] == 'A' {
		replacement = 'B'
	}
	corrupted := []byte(text)
	corrupted[position] = replacement
	_, err = ParseShare(string(corrupted))
	require.Error(t, err)
	require.True(t, strings.Contains(FormatError(err), "checksum") || strings.Contains(FormatError(err), "format"), FormatError(err))
}

func TestShareTextNormalization(t *testing.T) {
	secret := bytes.Repeat([]byte{0x44}, 32)
	shares, err := Split(secret, DefaultThreshold, DefaultTotal)
	require.NoError(t, err)
	defer clearShares(shares)

	canonical := shares[0].String()
	lower := strings.ToLower(canonical)
	spaced := strings.ReplaceAll(canonical, "-", " \t ")
	for _, variant := range []string{lower, spaced, "  " + canonical + "\n"} {
		parsed, err := ParseShare(variant)
		require.NoError(t, err)
		require.Equal(t, shares[0].Index, parsed.Index)
		require.Equal(t, shares[0].Data, parsed.Data)
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{
		"",
		"not-a-share",
		"S2V1-",
		"S2V1-!!!!",
		"S2V1-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		_, err := ParseShare(input)
		require.Error(t, err, "input %q must be rejected", input)
	}
}

func TestSplitParametersValidated(t *testing.T) {
	secret := bytes.Repeat([]byte{0x55}, 32)
	for _, params := range [][2]int{{1, 3}, {3, 2}, {2, 0}, {2, 256}} {
		_, err := Split(secret, params[0], params[1])
		require.ErrorIs(t, err, ErrParameters)
	}
	_, err := Split(nil, 2, 3)
	require.ErrorIs(t, err, ErrParameters)
	_, err = Split(make([]byte, maxShareData+1), 2, 3)
	require.ErrorIs(t, err, ErrParameters)
}

// TestGoldenVector pins the wire format and the GF(256) arithmetic with a
// hand-computed split: secret {0x01,0x02} and a single random coefficient
// {0x01,0x01} produces f(x) = secret + x, so
//
//	share 1 = {0x01^0x01, 0x02^0x01} = {0x00, 0x03}
//	share 2 = {0x01^0x02, 0x02^0x02} = {0x03, 0x00}
//	share 3 = {0x01^0x03, 0x02^0x03} = {0x02, 0x01}
func TestGoldenVector(t *testing.T) {
	secret := []byte{0x01, 0x02}
	fixed := bytes.NewReader([]byte{0x01, 0x01})
	shares, err := split(secret, 2, 3, fixed)
	require.NoError(t, err)
	defer clearShares(shares)

	require.Equal(t, []byte{0x00, 0x03}, shares[0].Data)
	require.Equal(t, []byte{0x03, 0x00}, shares[1].Data)
	require.Equal(t, []byte{0x02, 0x01}, shares[2].Data)

	// The encoded strings are generated from the format above; pinning them
	// catches accidental changes to prefix, checksum or grouping.
	require.Equal(t, "S2V1-AEBA-GAIC-AAB6-ZCMX-QY", shares[0].String())

	recovered, err := Combine([]Share{shares[1], shares[0]})
	require.NoError(t, err)
	require.Equal(t, secret, recovered)
	clear(recovered)
}

func TestCommitmentIsDeterministicAndSeparated(t *testing.T) {
	first := Commitment([]byte(strings.Repeat("a", 32)))
	second := Commitment([]byte(strings.Repeat("a", 32)))
	third := Commitment([]byte(strings.Repeat("b", 32)))
	require.Equal(t, first, second)
	require.NotEqual(t, first, third)
}

func TestFormatErrorKeepsDetailInternal(t *testing.T) {
	require.Equal(t, "checksum", FormatError(ErrChecksum))
	require.Equal(t, "threshold", FormatError(ErrThreshold))
	require.Equal(t, "format", FormatError(nil))
}
