package service

import "testing"

func TestInferGoogleOneTier(t *testing.T) {
	tests := []struct {
		name         string
		storageBytes int64
		expectedTier string
	}{
		{"Negative storage", -1, TierGoogleOneUnknown},
		{"Zero storage", 0, TierGoogleOneUnknown},

		// Free tier boundary (15GB)
		{"Below free tier", 10 * GB, TierGoogleOneUnknown},
		{"Just below free tier", StorageTierFree - 1, TierGoogleOneUnknown},
		{"Free tier (15GB)", StorageTierFree, TierFree},

		// Basic tier boundary (100GB)
		{"Between free and basic", 50 * GB, TierFree},
		{"Just below basic tier", StorageTierBasic - 1, TierFree},
		{"Basic tier (100GB)", StorageTierBasic, TierGoogleOneBasic},

		// Standard tier boundary (200GB)
		{"Between basic and standard", 150 * GB, TierGoogleOneBasic},
		{"Just below standard tier", StorageTierStandard - 1, TierGoogleOneBasic},
		{"Standard tier (200GB)", StorageTierStandard, TierGoogleOneStandard},

		// AI Premium tier boundary (2TB)
		{"Between standard and premium", 1 * TB, TierGoogleOneStandard},
		{"Just below AI Premium tier", StorageTierAIPremium - 1, TierGoogleOneStandard},
		{"AI Premium tier (2TB)", StorageTierAIPremium, TierAIPremium},

		// Unlimited tier boundary (> 100TB)
		{"Between premium and unlimited", 50 * TB, TierAIPremium},
		{"At unlimited threshold (100TB)", StorageTierUnlimited, TierAIPremium},
		{"Unlimited tier (100TB+)", StorageTierUnlimited + 1, TierGoogleOneUnlimited},
		{"Unlimited tier (101TB+)", 101 * TB, TierGoogleOneUnlimited},
		{"Very large storage", 1000 * TB, TierGoogleOneUnlimited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferGoogleOneTier(tt.storageBytes)
			if result != tt.expectedTier {
				t.Errorf("inferGoogleOneTier(%d) = %s, want %s",
					tt.storageBytes, result, tt.expectedTier)
			}
		})
	}
}
