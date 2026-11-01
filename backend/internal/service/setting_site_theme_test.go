package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type siteThemeRepoStub struct {
	SettingRepository
	values   map[string]string
	writes   []map[string]string
	getErr   error
	writeErr error
}

func (r *siteThemeRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}

func (r *siteThemeRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *siteThemeRepoStub) SetMultiple(_ context.Context, updates map[string]string) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	r.writes = append(r.writes, updates)
	if r.values == nil {
		r.values = map[string]string{}
	}
	for key, value := range updates {
		r.values[key] = value
	}
	return nil
}

func siteThemePtr[T any](v T) *T { return &v }

func TestDefaultSiteThemeSettingsUseSakuraPrimary(t *testing.T) {
	require.Equal(t, SiteThemeSettings{Primary: "sakura", Neutral: "neutral", Radius: 0.375, Font: "system", DefaultMode: "system"}, DefaultSiteThemeSettings())
}

func TestValidateSiteThemePatchAcceptsEveryAllowlistedValue(t *testing.T) {
	require.Len(t, siteThemePrimaryOptions, 19)
	for _, v := range siteThemePrimaryOptions {
		require.NoError(t, ValidateSiteThemePatch(SiteThemePatch{Primary: siteThemePtr(v)}), v)
	}
	for _, v := range siteThemeNeutralOptions {
		require.NoError(t, ValidateSiteThemePatch(SiteThemePatch{Neutral: siteThemePtr(v)}), v)
	}
	for _, v := range siteThemeRadiusOptions {
		require.NoError(t, ValidateSiteThemePatch(SiteThemePatch{Radius: siteThemePtr(v)}), v)
	}
	for _, v := range siteThemeFontOptions {
		require.NoError(t, ValidateSiteThemePatch(SiteThemePatch{Font: siteThemePtr(v)}), v)
	}
	for _, v := range siteThemeModeOptions {
		require.NoError(t, ValidateSiteThemePatch(SiteThemePatch{DefaultMode: siteThemePtr(v)}), v)
	}
	require.NoError(t, ValidateSiteThemePatch(SiteThemePatch{}))
}

func TestValidateSiteThemePatchRejectsValuesOutsideAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name   string
		patch  SiteThemePatch
		reason string
	}{
		{"css injection", SiteThemePatch{Primary: siteThemePtr("red;}body{display:none")}, "INVALID_THEME_PRIMARY"},
		{"raw color", SiteThemePatch{Primary: siteThemePtr("#ff0000")}, "INVALID_THEME_PRIMARY"},
		{"case", SiteThemePatch{Primary: siteThemePtr("Red")}, "INVALID_THEME_PRIMARY"},
		{"padding", SiteThemePatch{Primary: siteThemePtr(" red")}, "INVALID_THEME_PRIMARY"},
		{"empty", SiteThemePatch{Primary: siteThemePtr("")}, "INVALID_THEME_PRIMARY"},
		{"neutral as primary", SiteThemePatch{Primary: siteThemePtr("slate")}, "INVALID_THEME_PRIMARY"},
		{"primary as neutral", SiteThemePatch{Neutral: siteThemePtr("red")}, "INVALID_THEME_NEUTRAL"},
		{"radius too large", SiteThemePatch{Radius: siteThemePtr(1.0)}, "INVALID_THEME_RADIUS"},
		{"radius negative", SiteThemePatch{Radius: siteThemePtr(-0.125)}, "INVALID_THEME_RADIUS"},
		{"radius off grid", SiteThemePatch{Radius: siteThemePtr(0.3)}, "INVALID_THEME_RADIUS"},
		{"radius nan", SiteThemePatch{Radius: siteThemePtr(math.NaN())}, "INVALID_THEME_RADIUS"},
		{"radius inf", SiteThemePatch{Radius: siteThemePtr(math.Inf(1))}, "INVALID_THEME_RADIUS"},
		{"font url", SiteThemePatch{Font: siteThemePtr("url(https://evil.example/x.woff2)")}, "INVALID_THEME_FONT"},
		{"font family name", SiteThemePatch{Font: siteThemePtr("Inter")}, "INVALID_THEME_FONT"},
		{"mode", SiteThemePatch{DefaultMode: siteThemePtr("auto")}, "INVALID_THEME_DEFAULT_MODE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSiteThemePatch(tc.patch)
			require.Error(t, err)
			require.Equal(t, tc.reason, infraerrors.Reason(err))
		})
	}
}

func TestParseSiteThemeSettingsFallsBackPerField(t *testing.T) {
	require.Equal(t, DefaultSiteThemeSettings(), parseSiteThemeSettings(nil))
	got := parseSiteThemeSettings(map[string]string{
		SettingKeyThemePrimary:     "sakura",
		SettingKeyThemeNeutral:     "<script>",
		SettingKeyThemeRadius:      "0",
		SettingKeyThemeFont:        "comic-sans",
		SettingKeyThemeDefaultMode: "dark",
	})
	require.Equal(t, SiteThemeSettings{Primary: "sakura", Neutral: "neutral", Radius: 0, Font: "system", DefaultMode: "dark"}, got)
	for _, raw := range []string{"", "0.3", "1", "NaN", "abc", "0.375rem"} {
		require.Equal(t, 0.375, parseSiteThemeSettings(map[string]string{SettingKeyThemeRadius: raw}).Radius, raw)
	}
	negativeZero := parseSiteThemeSettings(map[string]string{SettingKeyThemeRadius: "-0"}).Radius
	require.False(t, math.Signbit(negativeZero))
}

func TestUpdateSiteThemeWritesOnlyPatchedKeys(t *testing.T) {
	repo := &siteThemeRepoStub{values: map[string]string{SettingKeyThemePrimary: "green", SettingKeyThemeFont: "inter"}}
	svc := &SettingService{settingRepo: repo}
	updates := 0
	svc.SetOnUpdateCallback(func() { updates++ })

	got, err := svc.UpdateSiteTheme(context.Background(), SiteThemePatch{Neutral: siteThemePtr("zinc"), Radius: siteThemePtr(math.Copysign(0, -1))})
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{SettingKeyThemeNeutral: "zinc", SettingKeyThemeRadius: "0"}}, repo.writes)
	require.Equal(t, SiteThemeSettings{Primary: "green", Neutral: "zinc", Radius: 0, Font: "inter", DefaultMode: "system"}, got)
	require.Equal(t, 1, updates)
}

func TestUpdateSiteThemeRejectsInvalidPatchWithoutWriting(t *testing.T) {
	repo := &siteThemeRepoStub{values: map[string]string{SettingKeyThemePrimary: "green"}}
	svc := &SettingService{settingRepo: repo}
	_, err := svc.UpdateSiteTheme(context.Background(), SiteThemePatch{Neutral: siteThemePtr("zinc"), Primary: siteThemePtr("javascript:alert(1)")})
	require.Equal(t, "INVALID_THEME_PRIMARY", infraerrors.Reason(err))
	require.Empty(t, repo.writes)
	require.Equal(t, "green", repo.values[SettingKeyThemePrimary])
}

func TestUpdateSiteThemeEmptyPatchAndStorageErrors(t *testing.T) {
	repo := &siteThemeRepoStub{}
	svc := &SettingService{settingRepo: repo}
	svc.SetOnUpdateCallback(func() { t.Fatal("empty patch must not invalidate caches") })
	got, err := svc.UpdateSiteTheme(context.Background(), SiteThemePatch{})
	require.NoError(t, err)
	require.Equal(t, DefaultSiteThemeSettings(), got)
	require.Empty(t, repo.writes)

	repo.writeErr = errors.New("db down")
	_, err = svc.UpdateSiteTheme(context.Background(), SiteThemePatch{Font: siteThemePtr("inter")})
	require.ErrorContains(t, err, "db down")

	repo.getErr = errors.New("db down")
	_, err = svc.GetSiteTheme(context.Background())
	require.ErrorContains(t, err, "db down")
}

func TestPublicSettingsExposeSiteTheme(t *testing.T) {
	repo := &siteThemeRepoStub{values: map[string]string{
		SettingKeyThemePrimary: "teal",
		SettingKeyThemeNeutral: "stone",
		SettingKeyThemeRadius:  "0.5",
		SettingKeyThemeFont:    "not-a-font",
	}}
	svc := NewSettingService(repo, &config.Config{})
	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	want := SiteThemeSettings{Primary: "teal", Neutral: "stone", Radius: 0.5, Font: "system", DefaultMode: "system"}
	require.Equal(t, want, settings.SiteTheme)

	injected, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(injected)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "teal", payload["theme_primary"])
	require.Equal(t, "stone", payload["theme_neutral"])
	require.Equal(t, 0.5, payload["theme_radius"])
	require.Equal(t, "system", payload["theme_font"])
	require.Equal(t, "system", payload["theme_default_mode"])
}
