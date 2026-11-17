//go:build unit

package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminDeepSeekAudioPriceResetAndGrokCompatibility(t *testing.T) {
	for _, platform := range []string{PlatformDeepseek, PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			group := &Group{ID: 36, Platform: platform, Status: StatusActive,
				AudioTTSPricePerMillionChars: float64Ptr(2), AudioSTTPricePerHour: float64Ptr(3)}
			repo := &groupRepoStubForAdmin{getByID: group}
			svc := &adminServiceImpl{groupRepo: repo}
			updated, err := svc.UpdateGroup(t.Context(), 36, &UpdateGroupInput{AudioTTSPricePerMillionCharsSet: true, AudioSTTPricePerHourSet: true})
			require.NoError(t, err)
			if platform == PlatformDeepseek {
				require.Nil(t, updated.AudioTTSPricePerMillionChars)
				require.Nil(t, updated.AudioSTTPricePerHour)
			} else {
				require.Equal(t, 2.0, *updated.AudioTTSPricePerMillionChars, "Grok null keeps its existing behavior")
				require.Equal(t, 3.0, *updated.AudioSTTPricePerHour)
			}
			_, err = svc.UpdateGroup(t.Context(), 36, &UpdateGroupInput{AudioTTSPricePerMillionChars: float64Ptr(-1)})
			// Negative is the "clear" signal of the admin form on every platform.
			require.NoError(t, err)
			require.Nil(t, group.AudioTTSPricePerMillionChars, "negative resets the price")
		})
	}
	for _, price := range []float64{math.NaN(), math.Inf(1)} {
		svc := &adminServiceImpl{}
		_, err := svc.CreateGroup(t.Context(), &CreateGroupInput{Platform: PlatformDeepseek, AudioTTSPricePerMillionChars: &price})
		require.Error(t, err)
		_, err = svc.CreateGroup(t.Context(), &CreateGroupInput{Platform: PlatformDeepseek, AudioSTTPricePerHour: &price})
		require.Error(t, err)
	}
}

func TestAdminGroupOCRPriceCreateUpdateAndNullReset(t *testing.T) {
	for _, price := range []*float64{nil, float64Ptr(0), float64Ptr(.225)} {
		repo := &groupRepoStubForAdmin{}
		svc := &adminServiceImpl{groupRepo: repo}
		group, err := svc.CreateGroup(t.Context(), &CreateGroupInput{Name: "ocr", Platform: PlatformDeepseek, RateMultiplier: 1, OCRPricePer1kPages: price})
		require.NoError(t, err)
		require.Equal(t, price, group.OCRPricePer1kPages)
		repo.getByID = group
		group, err = svc.UpdateGroup(t.Context(), 1, &UpdateGroupInput{OCRPricePer1kPages: float64Ptr(2)})
		require.NoError(t, err)
		require.Equal(t, 2.0, *group.OCRPricePer1kPages)
		group, err = svc.UpdateGroup(t.Context(), 1, &UpdateGroupInput{})
		require.NoError(t, err)
		require.Equal(t, 2.0, *group.OCRPricePer1kPages, "omission must not clear a saved price")
		group, err = svc.UpdateGroup(t.Context(), 1, &UpdateGroupInput{OCRPricePer1kPagesSet: true})
		require.NoError(t, err)
		require.Nil(t, group.OCRPricePer1kPages, "explicit null restores the default")
		_, err = svc.UpdateGroup(t.Context(), 1, &UpdateGroupInput{OCRPricePer1kPages: float64Ptr(2)})
		require.NoError(t, err)
		group, err = svc.UpdateGroup(t.Context(), 1, &UpdateGroupInput{OCRPricePer1kPages: float64Ptr(-1), OCRPricePer1kPagesSet: true})
		require.NoError(t, err)
		require.Nil(t, group.OCRPricePer1kPages, "-1 from the admin form restores the default too")
	}
	for _, price := range []float64{math.NaN(), math.Inf(1)} {
		svc := &adminServiceImpl{}
		_, err := svc.CreateGroup(t.Context(), &CreateGroupInput{OCRPricePer1kPages: &price})
		require.Error(t, err)
		_, err = svc.UpdateGroup(t.Context(), 1, &UpdateGroupInput{OCRPricePer1kPages: &price})
		require.Error(t, err)
	}
}
