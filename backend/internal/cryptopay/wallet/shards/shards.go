// Package shards implements a versioned 2-of-3 Shamir secret sharing scheme
// over GF(256). It is the wire-compatible sibling of the browser implementation
// in the admin frontend: both sides must produce identical share strings.
//
// It is deliberately not SLIP-39: SLIP-39 adds a passphrase, Feistel
// encryption and a 1024-word wordlist, and no audited Go + browser pair
// exists for byte-identical output. This scheme keeps the security-relevant
// properties (GF(256) Shamir sharing, threshold 2 of 3, versioned share with a
// checksum, order-independent recovery) while staying small enough to audit on
// both sides.
//
// Share text format:
//
//	S2V1-XXXX-XXXX-... (base32, RFC 4648, no padding, grouped in fours)
//
// Decoded payload:
//
//	version(1) | threshold(1) | total(1) | index(1) | dataLen(1) | data(n) | checksum(4)
//
// The checksum is the first four bytes of SHA-256 over everything before it.
// A share is only useful together with one other share from the same split;
// a single share reveals nothing about the secret.
package shards

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"io"
	"sort"
	"strings"
)

const (
	// Scheme is persisted next to the share so a future format can migrate.
	Scheme = "shamir-gf256"
	// Version is the share payload version.
	Version = 1
	// DefaultThreshold and DefaultTotal are the only combination this
	// application accepts; the format still carries them for validation.
	DefaultThreshold = 2
	DefaultTotal     = 3

	textPrefix    = "S2V1"
	checksumBytes = 4
	headerBytes   = 5 // version, threshold, total, index, data length
	maxShareData  = 255
)

var (
	ErrFormat     = errors.New("share format is invalid")
	ErrVersion    = errors.New("share version is unsupported")
	ErrChecksum   = errors.New("share checksum does not match")
	ErrThreshold  = errors.New("at least two different shares are required")
	ErrDuplicate  = errors.New("duplicate share index")
	ErrParameters = errors.New("share parameters are invalid")
)

// Share is one point of the split secret.
type Share struct {
	Version   byte
	Threshold byte
	Total     byte
	Index     byte
	Data      []byte
}

// Commitment is the public fingerprint of the data key. It lets the server
// verify a reconstructed key without ever storing the key itself.
func Commitment(secret []byte) [32]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte("sub2api/crypto-wallet/dk-commitment/v1:"))
	_, _ = hash.Write(secret)
	var out [32]byte
	copy(out[:], hash.Sum(nil))
	return out
}

// Split shares secret into total shares with the given threshold. The caller
// must clear the returned shares after they have been shown once.
func Split(secret []byte, threshold, total int) ([]Share, error) {
	return split(secret, threshold, total, rand.Reader)
}

func split(secret []byte, threshold, total int, randomness io.Reader) ([]Share, error) {
	if len(secret) == 0 || len(secret) > maxShareData {
		return nil, ErrParameters
	}
	if threshold < 2 || total < threshold || total > 255 || threshold > 255 {
		return nil, ErrParameters
	}
	if randomness == nil {
		return nil, ErrParameters
	}
	shares := make([]Share, total)
	for i := range shares {
		shares[i] = Share{
			Version:   Version,
			Threshold: byte(threshold),
			Total:     byte(total),
			Index:     byte(i + 1),
			Data:      make([]byte, len(secret)),
		}
	}
	coefficients := make([]byte, threshold)
	for byteIndex, value := range secret {
		coefficients[0] = value
		if threshold > 1 {
			if _, err := io.ReadFull(randomness, coefficients[1:]); err != nil {
				clear(coefficients)
				for i := range shares {
					clear(shares[i].Data)
				}
				return nil, ErrParameters
			}
		}
		for shareIndex := range shares {
			shares[shareIndex].Data[byteIndex] = evaluate(coefficients, shares[shareIndex].Index)
		}
	}
	clear(coefficients)
	return shares, nil
}

// Combine recovers the secret from any threshold shares. Order does not
// matter; duplicate indexes and shares from different splits are rejected.
func Combine(shares []Share) ([]byte, error) {
	if len(shares) < DefaultThreshold {
		return nil, ErrThreshold
	}
	version := shares[0].Version
	threshold := shares[0].Threshold
	total := shares[0].Total
	length := len(shares[0].Data)
	seen := make(map[byte]bool, len(shares))
	for _, share := range shares {
		if share.Version != version || share.Threshold != threshold || share.Total != total || len(share.Data) != length {
			return nil, ErrParameters
		}
		if version != Version || threshold < 2 || total < threshold || share.Index == 0 || share.Index > total || length == 0 {
			return nil, ErrParameters
		}
		if seen[share.Index] {
			return nil, ErrDuplicate
		}
		seen[share.Index] = true
	}
	ordered := append([]Share(nil), shares...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	secret := make([]byte, length)
	for byteIndex := 0; byteIndex < length; byteIndex++ {
		// Lagrange interpolation at x=0 over the supplied points.
		var value byte
		for i, point := range ordered {
			var basis byte = 1
			for j, other := range ordered {
				if i == j {
					continue
				}
				numerator := other.Index
				denominator := other.Index ^ point.Index
				if denominator == 0 {
					clear(secret)
					return nil, ErrDuplicate
				}
				basis = mul(basis, div(numerator, denominator))
			}
			value ^= mul(point.Data[byteIndex], basis)
		}
		secret[byteIndex] = value
	}
	return secret, nil
}

// evaluate runs Horner's method over the coefficient list at x.
func evaluate(coefficients []byte, x byte) byte {
	var result byte
	for i := len(coefficients) - 1; i >= 0; i-- {
		result = mul(result, x) ^ coefficients[i]
	}
	return result
}

// gfExp/gfLog implement multiplication in GF(2^8) with the AES reduction
// polynomial 0x11b and generator 0x03.
var gfExp [512]byte
var gfLog [256]byte

func init() {
	x := byte(1)
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = byte(i)
		x = mulByThree(x)
	}
	for i := 255; i < len(gfExp); i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func mulByThree(x byte) byte {
	doubled := x << 1
	if x&0x80 != 0 {
		doubled ^= 0x1b
	}
	return doubled ^ x
}

func mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func div(a, b byte) byte {
	if a == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])-int(gfLog[b])+255]
}

// String renders the share as grouped base32 with a version prefix.
func (s Share) String() string {
	payload := s.encode()
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(payload)
	var builder strings.Builder
	_, _ = builder.WriteString(textPrefix)
	for i := 0; i < len(encoded); i += 4 {
		_, _ = builder.WriteString("-")
		end := i + 4
		if end > len(encoded) {
			end = len(encoded)
		}
		_, _ = builder.WriteString(encoded[i:end])
	}
	return builder.String()
}

func (s Share) encode() []byte {
	payload := make([]byte, headerBytes+len(s.Data)+checksumBytes)
	payload[0] = s.Version
	payload[1] = s.Threshold
	payload[2] = s.Total
	payload[3] = s.Index
	payload[4] = byte(len(s.Data))
	copy(payload[headerBytes:], s.Data)
	checksum := sha256.Sum256(payload[:headerBytes+len(s.Data)])
	copy(payload[headerBytes+len(s.Data):], checksum[:checksumBytes])
	return payload
}

// ParseShare accepts the grouped form, ignores spaces and dashes, and is
// case-insensitive. It rejects any share whose checksum does not match, so an
// operator typo never reaches key combination.
func ParseShare(text string) (Share, error) {
	normalized := strings.ToUpper(strings.TrimSpace(text))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "\t", "")
	normalized = strings.ReplaceAll(normalized, "\r", "")
	normalized = strings.ReplaceAll(normalized, "\n", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	if !strings.HasPrefix(normalized, textPrefix) {
		return Share{}, ErrFormat
	}
	encoded := strings.TrimPrefix(normalized, textPrefix)
	payload, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(encoded)
	if err != nil {
		return Share{}, ErrFormat
	}
	if len(payload) < headerBytes+1+checksumBytes {
		return Share{}, ErrFormat
	}
	body := payload[:len(payload)-checksumBytes]
	checksum := payload[len(payload)-checksumBytes:]
	expected := sha256.Sum256(body)
	if !constantTimeEqual(checksum, expected[:checksumBytes]) {
		return Share{}, ErrChecksum
	}
	if body[0] != Version {
		return Share{}, ErrVersion
	}
	threshold, total, index := body[1], body[2], body[3]
	dataLength := int(body[4])
	if threshold < 2 || total < threshold || index == 0 || index > total || dataLength == 0 || len(body) != headerBytes+dataLength {
		return Share{}, ErrParameters
	}
	data := make([]byte, dataLength)
	copy(data, body[headerBytes:])
	return Share{Version: body[0], Threshold: threshold, Total: total, Index: index, Data: data}, nil
}

// ParseAndCombine is the one call the server uses for an operator-supplied
// share list. It parses every share, then combines exactly the distinct ones.
func ParseAndCombine(texts []string) ([]byte, error) {
	if len(texts) < DefaultThreshold || len(texts) > 255 {
		return nil, ErrThreshold
	}
	shares := make([]Share, 0, len(texts))
	for _, text := range texts {
		share, err := ParseShare(text)
		if err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	return Combine(shares)
}

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}

// FormatError keeps parser detail away from HTTP responses while remaining
// usable in audit reasons.
func FormatError(err error) string {
	switch {
	case errors.Is(err, ErrChecksum):
		return "checksum"
	case errors.Is(err, ErrVersion):
		return "version"
	case errors.Is(err, ErrThreshold):
		return "threshold"
	case errors.Is(err, ErrDuplicate):
		return "duplicate"
	case errors.Is(err, ErrParameters):
		return "parameters"
	default:
		return "format"
	}
}
