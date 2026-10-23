package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObserverUsageCannotSerializeCustomerOrUpstreamSecrets(t *testing.T) {
	secret := "fixture-private-personal-value"
	groupID := int64(4)
	log := AdminUsageLog{UsageLog: UsageLog{ID: 99, UserID: 2, APIKeyID: 3, AccountID: 4, GroupID: &groupID,
		Model: "public-model", InputTokens: 123, ActualCost: 1.25, UserAgent: &secret, IPAddress: &secret, SessionID: &secret, RequestID: secret,
		User:   &User{ID: 2, Email: secret, Username: secret, Balance: 500},
		APIKey: &APIKey{ID: 3, Name: secret, Key: secret},
		Group:  &Group{ID: 4, Name: secret},
	}, IPAddress: &secret, Account: &AccountSummary{ID: 4, Name: secret}, UpstreamRequestID: &secret, ModelMappingChain: &secret}
	encoded, err := json.Marshal(UsageLogForObserver(log))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("observer response leaked private field: %s", encoded)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user_agent", "ip_address", "session_id", "request_id", "upstream_request_id", "subscription", "model_mapping_chain"} {
		if _, exists := result[name]; exists {
			t.Fatalf("private field %s remains in output", name)
		}
	}
	if result["actual_cost"] != 1.25 || result["input_tokens"] != float64(123) {
		t.Fatal("usage metrics were changed")
	}
	apiKey, ok := result["api_key"].(map[string]any)
	if !ok {
		t.Fatal("anonymized API key summary is missing")
	}
	if apiKey["key"] != nil {
		t.Fatal("API key secret is serialized")
	}
	if log.User.Email != secret || log.APIKey.Key != secret || log.Account.Name != secret {
		t.Fatal("redaction mutated an owner/shared snapshot")
	}
}
