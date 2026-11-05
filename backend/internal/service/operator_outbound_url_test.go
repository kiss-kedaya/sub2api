package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func outboundPolicyConfig(allow config.URLAllowlistConfig) *config.Config {
	return &config.Config{Security: config.SecurityConfig{URLAllowlist: allow}}
}

func TestValidateOperatorOutboundURLWithAllowlist(t *testing.T) {
	cfg := outboundPolicyConfig(config.URLAllowlistConfig{
		Enabled:       true,
		UpstreamHosts: []string{"up.example.com", "*.partner.example.org", "10.0.0.5"},
	})

	got, err := ValidateOperatorOutboundURL(cfg, "upstream site", "  https://up.example.com/  ")
	require.NoError(t, err)
	require.Equal(t, "https://up.example.com", got, "surrounding space and the trailing slash are removed")

	got, err = ValidateOperatorOutboundURL(cfg, "upstream site", "https://api.partner.example.org/base/")
	require.NoError(t, err)
	require.Equal(t, "https://api.partner.example.org/base", got, "a wildcard entry matches its sub-domains and keeps the path")

	for name, raw := range map[string]string{
		"host not on the list":       "https://evil.example.net",
		"cloud metadata address":     "https://169.254.169.254/latest/meta-data",
		"plain http":                 "http://up.example.com",
		"private literal on list":    "https://10.0.0.5",
		"loopback":                   "https://127.0.0.1",
		"lookalike of a listed host": "https://up.example.com.evil.example.net",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateOperatorOutboundURL(cfg, "upstream site", raw)
			require.Error(t, err)
			require.ErrorContains(t, err, "upstream site target rejected by URL security policy")
		})
	}

	// The operator can allow private hosts explicitly (an internal relay).
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	got, err = ValidateOperatorOutboundURL(cfg, "upstream site", "https://10.0.0.5")
	require.NoError(t, err)
	require.Equal(t, "https://10.0.0.5", got)
}

func TestValidateOperatorOutboundURLNeedsAHostListWhenTheAllowlistIsOn(t *testing.T) {
	cfg := outboundPolicyConfig(config.URLAllowlistConfig{Enabled: true})
	_, err := ValidateOperatorOutboundURL(cfg, "captcha endpoint", "https://api.example.com")
	require.ErrorContains(t, err, "captcha endpoint target rejected by URL security policy")
	require.ErrorContains(t, err, "allowlist is not configured")
}

func TestValidateOperatorOutboundURLFormatOnlyWhenTheAllowlistIsOff(t *testing.T) {
	off := outboundPolicyConfig(config.URLAllowlistConfig{})

	got, err := ValidateOperatorOutboundURL(off, "upstream site", "https://any.example.net/")
	require.NoError(t, err)
	require.Equal(t, "https://any.example.net", got)

	for name, raw := range map[string]string{
		"plain http by default": "http://any.example.net",
		"no scheme":             "any.example.net",
		"other scheme":          "ftp://any.example.net",
		"no host":               "https://",
		"port out of range":     "https://any.example.net:70000",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateOperatorOutboundURL(off, "upstream site", raw)
			require.ErrorContains(t, err, "upstream site target rejected by URL security policy")
		})
	}

	insecure := outboundPolicyConfig(config.URLAllowlistConfig{AllowInsecureHTTP: true})
	got, err = ValidateOperatorOutboundURL(insecure, "upstream site", "http://any.example.net")
	require.NoError(t, err)
	require.Equal(t, "http://any.example.net", got)

	// No config at all behaves like the allowlist being off.
	got, err = ValidateOperatorOutboundURL(nil, "upstream site", "https://any.example.net")
	require.NoError(t, err)
	require.Equal(t, "https://any.example.net", got)
	_, err = ValidateOperatorOutboundURL(nil, "upstream site", "http://any.example.net")
	require.Error(t, err)
}

func TestValidateOperatorOutboundURLNamesWhatItWasChecking(t *testing.T) {
	_, err := ValidateOperatorOutboundURL(nil, "captcha endpoint", "   ")
	require.EqualError(t, err, "captcha endpoint url is required")
}

// The CN probe keeps the exact wording it always had.
func TestCNProbeURLWordingIsUnchanged(t *testing.T) {
	_, err := cnValidateProbeURL(nil, "")
	require.EqualError(t, err, "probe url is required")
	_, err = cnValidateProbeURL(outboundPolicyConfig(config.URLAllowlistConfig{Enabled: true, UpstreamHosts: []string{"a.example.com"}}), "https://b.example.com")
	require.ErrorContains(t, err, "probe target rejected by URL security policy")
}

// outboundTestEncryptor stands in for the AES encryptor. It is defined here, not
// borrowed from another test file, because those sit behind the "unit" build tag
// and this file must compile with and without it.
type outboundTestEncryptor struct{}

func (outboundTestEncryptor) Encrypt(plaintext string) (string, error) {
	return "ENC:" + plaintext, nil
}
func (outboundTestEncryptor) Decrypt(ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, "ENC:"), nil
}

func channelServiceWithPolicy(cfg *config.Config) *UpstreamChannelService {
	s := NewUpstreamChannelService(nil, outboundTestEncryptor{})
	if cfg != nil {
		s.SetOutboundURLPolicy(cfg)
	}
	return s
}

func channelInput(site string) UpstreamChannelInput {
	return UpstreamChannelInput{Name: "relay", SiteURL: site, Username: "ops@example.com", Password: "secret"}
}

func TestChannelSiteAddressFollowsTheOutboundPolicyWhenSaved(t *testing.T) {
	s := channelServiceWithPolicy(outboundPolicyConfig(config.URLAllowlistConfig{
		Enabled: true, UpstreamHosts: []string{"up.example.com"},
	}))

	channel, err := s.applyInput(nil, channelInput("https://up.example.com/"), true)
	require.NoError(t, err)
	require.Equal(t, "https://up.example.com", channel.SiteURL)

	for _, site := range []string{"https://evil.example.net", "http://169.254.169.254", "https://127.0.0.1:8402"} {
		_, err := s.applyInput(nil, channelInput(site), true)
		require.ErrorContains(t, err, "upstream site target rejected by URL security policy", site)
	}

	// Moving an existing channel to a refused address is refused too.
	existing := &UpstreamChannel{Name: "relay", SiteURL: "https://up.example.com", Username: "ops@example.com"}
	_, err = s.applyInput(existing, UpstreamChannelInput{SiteURL: "https://evil.example.net"}, false)
	require.ErrorContains(t, err, "upstream site target rejected by URL security policy")
	require.Equal(t, "https://up.example.com", existing.SiteURL, "a refused edit leaves the stored channel alone")
}

func TestChannelCaptchaEndpointFollowsTheOutboundPolicyWhenSaved(t *testing.T) {
	s := channelServiceWithPolicy(outboundPolicyConfig(config.URLAllowlistConfig{
		Enabled: true, UpstreamHosts: []string{"up.example.com", "solver.example.com"},
	}))
	input := channelInput("https://up.example.com")

	input.CaptchaEndpoint = "https://solver.example.com/api/"
	channel, err := s.applyInput(nil, input, true)
	require.NoError(t, err)
	require.Equal(t, "https://solver.example.com/api", channel.CaptchaEndpoint)

	input.CaptchaEndpoint = "https://evil.example.net/solve"
	_, err = s.applyInput(nil, input, true)
	require.ErrorContains(t, err, "captcha endpoint target rejected by URL security policy")

	// No endpoint at all means the provider's own default, which is not checked here.
	input.CaptchaEndpoint = ""
	_, err = s.applyInput(nil, input, true)
	require.NoError(t, err)
}

func TestEditingOtherFieldsDoesNotRecheckTheStoredAddress(t *testing.T) {
	s := channelServiceWithPolicy(outboundPolicyConfig(config.URLAllowlistConfig{
		Enabled: true, UpstreamHosts: []string{"up.example.com"},
	}))
	// Saved before the operator tightened the policy.
	existing := &UpstreamChannel{Name: "relay", SiteURL: "https://old.example.org", Username: "ops@example.com"}
	channel, err := s.applyInput(existing, UpstreamChannelInput{Notes: "renamed"}, false)
	require.NoError(t, err, "a notes edit must not be blocked by an address it does not change")
	require.Equal(t, "https://old.example.org", channel.SiteURL)
}

func TestChannelServiceWithoutAPolicyKeepsTheOldBehaviour(t *testing.T) {
	s := channelServiceWithPolicy(nil)
	channel, err := s.applyInput(nil, channelInput("http://127.0.0.1:9/"), true)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:9", channel.SiteURL)
	_, err = s.applyInput(nil, channelInput("ftp://example.net"), true)
	require.ErrorContains(t, err, "must start with http:// or https://")

	var missing *UpstreamChannelService
	require.NotPanics(t, func() { missing.SetOutboundURLPolicy(nil) })
}

func TestUpstreamChannelClientChecksTheAddressBeforeEveryRequest(t *testing.T) {
	var hits atomic.Int32
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3600},
		})
	})
	client := NewUpstreamChannelClient()

	client.SetURLCheck(func(string) (string, error) {
		return "", errors.New("upstream site target rejected by URL security policy")
	})
	_, err := client.Login(context.Background(), server.URL, "ops@example.com", "secret", "", "")
	require.ErrorContains(t, err, "rejected by URL security policy")
	require.EqualValues(t, 0, hits.Load(), "a refused address must not receive the login")

	// The checked, normalized address is the one that is used.
	client.SetURLCheck(func(raw string) (string, error) {
		require.Equal(t, "http://placeholder.invalid", raw)
		return server.URL + "/", nil
	})
	session, err := client.Login(context.Background(), "http://placeholder.invalid/", "ops@example.com", "secret", "", "")
	require.NoError(t, err)
	require.Equal(t, "access-1", session.AccessToken)
	require.EqualValues(t, 1, hits.Load())

	var missing *UpstreamChannelClient
	require.NotPanics(t, func() { missing.SetURLCheck(nil) })
}
