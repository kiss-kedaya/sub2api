package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Site theme setting keys. The frontend maps every stored value to a built-in
// palette, font stack or radius, so only the allowlisted values below can be
// written and anything else read back from storage falls back to the default.
const (
	SettingKeyThemePrimary     = "theme_primary"
	SettingKeyThemeNeutral     = "theme_neutral"
	SettingKeyThemeRadius      = "theme_radius"
	SettingKeyThemeFont        = "theme_font"
	SettingKeyThemeDefaultMode = "theme_default_mode"
)

var (
	siteThemePrimaryOptions = []string{
		"black", "red", "orange", "amber", "yellow", "lime", "green", "emerald", "teal", "cyan",
		"sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink", "rose", "sakura",
	}
	siteThemeNeutralOptions = []string{"slate", "gray", "zinc", "neutral", "stone"}
	siteThemeRadiusOptions  = []float64{0, 0.125, 0.25, 0.375, 0.5}
	siteThemeFontOptions    = []string{"system", "public-sans", "inter", "noto-sans-sc"}
	siteThemeModeOptions    = []string{"system", "light", "dark"}
)

// SiteThemeSettings is the admin-selected site theme. Radius is in rem.
type SiteThemeSettings struct {
	Primary     string  `json:"theme_primary"`
	Neutral     string  `json:"theme_neutral"`
	Radius      float64 `json:"theme_radius"`
	Font        string  `json:"theme_font"`
	DefaultMode string  `json:"theme_default_mode"`
}

// DefaultSiteThemeSettings matches the look the site shipped with before the
// theme became configurable.
func DefaultSiteThemeSettings() SiteThemeSettings {
	return SiteThemeSettings{Primary: "black", Neutral: "neutral", Radius: 0.375, Font: "system", DefaultMode: "system"}
}

// SiteThemePatch carries the fields of one update; nil leaves a field unchanged.
type SiteThemePatch struct {
	Primary     *string
	Neutral     *string
	Radius      *float64
	Font        *string
	DefaultMode *string
}

func (p SiteThemePatch) empty() bool {
	return p.Primary == nil && p.Neutral == nil && p.Radius == nil && p.Font == nil && p.DefaultMode == nil
}

func formatSiteThemeRadius(radius float64) string {
	return strconv.FormatFloat(radius, 'f', -1, 64)
}

func invalidSiteThemeValue(reason, field string, options []string) error {
	return infraerrors.BadRequest(reason, fmt.Sprintf("%s must be one of: %s", field, strings.Join(options, ", ")))
}

// siteThemePatchUpdates validates the patch strictly and returns the stored
// value for each field it sets.
func siteThemePatchUpdates(patch SiteThemePatch) (map[string]string, error) {
	updates := make(map[string]string, 5)
	if patch.Primary != nil {
		if !slices.Contains(siteThemePrimaryOptions, *patch.Primary) {
			return nil, invalidSiteThemeValue("INVALID_THEME_PRIMARY", SettingKeyThemePrimary, siteThemePrimaryOptions)
		}
		updates[SettingKeyThemePrimary] = *patch.Primary
	}
	if patch.Neutral != nil {
		if !slices.Contains(siteThemeNeutralOptions, *patch.Neutral) {
			return nil, invalidSiteThemeValue("INVALID_THEME_NEUTRAL", SettingKeyThemeNeutral, siteThemeNeutralOptions)
		}
		updates[SettingKeyThemeNeutral] = *patch.Neutral
	}
	if patch.Radius != nil {
		// Store the allowlist entry, not the input, so -0 is written as "0".
		index := slices.Index(siteThemeRadiusOptions, *patch.Radius)
		if index < 0 {
			labels := make([]string, len(siteThemeRadiusOptions))
			for i, radius := range siteThemeRadiusOptions {
				labels[i] = formatSiteThemeRadius(radius)
			}
			return nil, invalidSiteThemeValue("INVALID_THEME_RADIUS", SettingKeyThemeRadius, labels)
		}
		updates[SettingKeyThemeRadius] = formatSiteThemeRadius(siteThemeRadiusOptions[index])
	}
	if patch.Font != nil {
		if !slices.Contains(siteThemeFontOptions, *patch.Font) {
			return nil, invalidSiteThemeValue("INVALID_THEME_FONT", SettingKeyThemeFont, siteThemeFontOptions)
		}
		updates[SettingKeyThemeFont] = *patch.Font
	}
	if patch.DefaultMode != nil {
		if !slices.Contains(siteThemeModeOptions, *patch.DefaultMode) {
			return nil, invalidSiteThemeValue("INVALID_THEME_DEFAULT_MODE", SettingKeyThemeDefaultMode, siteThemeModeOptions)
		}
		updates[SettingKeyThemeDefaultMode] = *patch.DefaultMode
	}
	return updates, nil
}

// ValidateSiteThemePatch rejects any value outside the allowlists.
func ValidateSiteThemePatch(patch SiteThemePatch) error {
	_, err := siteThemePatchUpdates(patch)
	return err
}

func allowedOrDefault(value string, options []string, fallback string) string {
	if slices.Contains(options, value) {
		return value
	}
	return fallback
}

// parseSiteThemeSettings is tolerant: a missing or unknown stored value falls
// back to the default for that field only.
func parseSiteThemeSettings(values map[string]string) SiteThemeSettings {
	defaults := DefaultSiteThemeSettings()
	theme := SiteThemeSettings{
		Primary:     allowedOrDefault(values[SettingKeyThemePrimary], siteThemePrimaryOptions, defaults.Primary),
		Neutral:     allowedOrDefault(values[SettingKeyThemeNeutral], siteThemeNeutralOptions, defaults.Neutral),
		Radius:      defaults.Radius,
		Font:        allowedOrDefault(values[SettingKeyThemeFont], siteThemeFontOptions, defaults.Font),
		DefaultMode: allowedOrDefault(values[SettingKeyThemeDefaultMode], siteThemeModeOptions, defaults.DefaultMode),
	}
	if radius, err := strconv.ParseFloat(values[SettingKeyThemeRadius], 64); err == nil {
		if index := slices.Index(siteThemeRadiusOptions, radius); index >= 0 {
			theme.Radius = siteThemeRadiusOptions[index]
		}
	}
	return theme
}

func siteThemeSettingKeys() []string {
	return []string{SettingKeyThemePrimary, SettingKeyThemeNeutral, SettingKeyThemeRadius, SettingKeyThemeFont, SettingKeyThemeDefaultMode}
}

// GetSiteTheme reads the stored theme; storage errors are returned so the admin
// editor can refuse to save over values it could not load.
func (s *SettingService) GetSiteTheme(ctx context.Context) (SiteThemeSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultSiteThemeSettings(), nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, siteThemeSettingKeys())
	if err != nil {
		return SiteThemeSettings{}, fmt.Errorf("get site theme: %w", err)
	}
	return parseSiteThemeSettings(values), nil
}

// UpdateSiteTheme writes only the fields present in the patch, so two editors
// changing different fields cannot undo each other through a stale read.
func (s *SettingService) UpdateSiteTheme(ctx context.Context, patch SiteThemePatch) (SiteThemeSettings, error) {
	updates, err := siteThemePatchUpdates(patch)
	if err != nil {
		return SiteThemeSettings{}, err
	}
	if !patch.empty() {
		if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
			return SiteThemeSettings{}, fmt.Errorf("update site theme: %w", err)
		}
		// The injected window.__APP_CONFIG__ HTML carries the theme; drop it.
		if s.onUpdate != nil {
			s.onUpdate()
		}
	}
	return s.GetSiteTheme(ctx)
}
