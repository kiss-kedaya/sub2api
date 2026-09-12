package service

import "testing"

func TestIsCNProviderServableModel_EmptyMappingFamilies(t *testing.T) {
	t.Parallel()

	if isCNProviderServableModel(PlatformZhipu, "gpt-5.6") {
		t.Fatal("zhipu empty mapping must not admit gpt-5.6")
	}
	if !isCNProviderServableModel(PlatformZhipu, "glm-4.6") {
		t.Fatal("zhipu empty mapping must admit glm-4.6")
	}
	if isCNProviderServableModel(PlatformKimi, "gpt-5.6") {
		t.Fatal("kimi empty mapping must not admit gpt-5.6")
	}
	if !isCNProviderServableModel(PlatformKimi, "kimi-k2.5") {
		t.Fatal("kimi empty mapping must admit kimi-k2.5")
	}
	if isCNProviderServableModel(PlatformDeepseek, "gpt-5.6") {
		t.Fatal("deepseek empty mapping must not admit gpt-5.6")
	}
	if !isCNProviderServableModel(PlatformDeepseek, "deepseek-chat") {
		t.Fatal("deepseek empty mapping must admit deepseek-chat")
	}
}

func TestZhipuEmptyMappingIsModelSupported(t *testing.T) {
	t.Parallel()
	account := &Account{Platform: PlatformZhipu, Type: AccountTypeAPIKey, Credentials: map[string]any{}}
	if account.IsModelSupported("gpt-5.6") {
		t.Fatal("zhipu account with empty mapping must not support gpt-5.6")
	}
	if !account.IsModelSupported("glm-4.5") {
		t.Fatal("zhipu account with empty mapping must support glm-4.5")
	}
}

func TestAccountsSupportingRequestedModel_DropsForeignEmptyMapping(t *testing.T) {
	t.Parallel()
	accounts := []Account{
		{ID: 1, Platform: PlatformZhipu, Type: AccountTypeAPIKey, Credentials: map[string]any{}},
	}
	got := accountsSupportingRequestedModel(accounts, "gpt-5.6")
	if len(got) != 0 {
		t.Fatalf("expected no zhipu empty-mapping accounts for gpt-5.6, got %d", len(got))
	}
	filtered, unsupported := filterAccountsSupportingRequestedModel(accounts, "gpt-5.6")
	if len(filtered) != 0 || unsupported != 1 {
		t.Fatalf("filtered=%d unsupported=%d, want 0/1", len(filtered), unsupported)
	}
}
