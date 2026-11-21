package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const modelsPricingPreviewTestSnapshot = `{
  "google":{"id":"google","models":{
    "gemini-4-argon":{"id":"gemini-4-argon","cost":{"input":1.25,"output":3,"cache_read":0,"cache_write":null}},
    "tiered":{"cost":{"input":1,"context_over_200k":{"input":2}}},
    "bad-value":{"cost":{"input":"1","output":2,"cache_read":-1}},
    "local-currency":{"cost":{"currency":"CNY","input":1}},
    "per-call":{"cost":{"unit":"USD/request","input":1}},
    "bad-alias":{"id":"different-id","cost":{"input":1}},
    "tiny":{"cost":{"input":1e-400}},
    "old-shape":{"pricing":{"input":1}},
    "null-cost":{"cost":null}
  }},
  "openai":{"id":"openai","models":{"no-cost":{}}},
  "google-vertex":{"id":"google-vertex","models":{"gemini-4-argon":{"cost":{"input":7}}}},
  "reseller":{"id":"google","models":{"gemini-4-neon":{"cost":{"input":9}},"gemini-4-argon":{"cost":{"input":9}}}}
}`

func TestModelsPricingPreviewCoverage(t *testing.T) {
	const inventory = `{"rows":[
	  {"row_id":"zero","provider_id":"google","billing_model_id":"gemini-4-argon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input","cache_read"]},
	  {"row_id":"neon","provider_id":"google","billing_model_id":"gemini-4-neon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]},
	  {"row_id":"no-alias","provider_id":"google","billing_model_id":"gemini-4-argon-high","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]},
	  {"row_id":"reseller","provider_id":"reseller","billing_model_id":"gemini-4-neon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]},
	  {"row_id":"cloud","provider_id":"google-vertex","billing_model_id":"gemini-4-argon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]},
	  {"row_id":"partial-card","provider_id":"google","billing_model_id":"gemini-4-argon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input","output","cache_write"],"explicit_overrides":{"input":"group"}},
	  {"row_id":"full-card","provider_id":"unknown","billing_model_id":"unlisted","mode":"image","currency":"CNY","unit":"per_image","required_fields":["image"],"explicit_overrides":{"image":"channel"}},
	  {"row_id":"same-id-other-provider","provider_id":"openai","billing_model_id":"gemini-4-argon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]}
	]}`
	report, err := PreviewModelsPricingCoverage([]byte(modelsPricingPreviewTestSnapshot), []byte(inventory))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := report.SnapshotSHA256, fmt.Sprintf("%x", sha256.Sum256([]byte(modelsPricingPreviewTestSnapshot))); got != want {
		t.Fatalf("snapshot hash = %s, want %s", got, want)
	}
	if got, want := report.InventorySHA256, fmt.Sprintf("%x", sha256.Sum256([]byte(inventory))); got != want {
		t.Fatalf("inventory hash = %s, want %s", got, want)
	}
	want := []struct{ id, status, overrides string }{
		{"zero", "comparable", "none"},
		{"neon", "missing", "none"},
		{"no-alias", "missing", "none"},
		{"reseller", "incompatible", "none"},
		{"cloud", "incompatible", "none"},
		{"partial-card", "missing", "partial"},
		{"full-card", "explicit_override", "full"},
		{"same-id-other-provider", "missing", "none"},
	}
	if len(report.Rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(report.Rows), len(want))
	}
	for i, expected := range want {
		row := report.Rows[i]
		if row.InputRow != i+1 || row.RowID != expected.id || row.Status != expected.status || row.OverrideCoverage != expected.overrides {
			t.Errorf("row %d = %#v, want row id=%s, status=%s, overrides=%s", i+1, row, expected.id, expected.status, expected.overrides)
		}
	}
	if got := report.Rows[0].Fields[1].Quote; got == nil || got.String() != "0" {
		t.Errorf("explicit zero = %v", got)
	}
	if got := report.Rows[1].Fields[0].Reason; got != "model_missing" {
		t.Errorf("neon reason = %q", got)
	}
	if got := report.Rows[3].Fields[0].Reason; got != "provider_not_first_party" {
		t.Errorf("reseller reason = %q", got)
	}
	partial := report.Rows[5].Fields
	if partial[0].Status != "explicit_override" || partial[0].OverrideSource != "group" || partial[0].Quote != nil ||
		partial[1].Status != "comparable" || partial[1].Quote == nil || partial[1].Quote.String() != "3" ||
		partial[2].Status != "missing" || partial[2].Reason != "price_field_missing" || partial[2].Quote != nil {
		t.Errorf("partial card fields = %#v", partial)
	}
	if report.Summary["comparable"] != 1 || report.Summary["missing"] != 4 || report.Summary["incompatible"] != 2 || report.Summary["explicit_override"] != 1 {
		t.Errorf("summary = %v", report.Summary)
	}
}

func TestModelsPricingPreviewIncompatibleQuotes(t *testing.T) {
	cases := []struct{ name, model, mode, currency, unit, field, structure, reason string }{
		{"currency", "gemini-4-argon", "token", "CNY", "per_million_tokens", "input", "", "inventory_currency_unsupported"},
		{"no units", "gemini-4-argon", "token", "USD", "", "input", "", "inventory_unit_unsupported"},
		{"per video second", "gemini-4-argon", "video", "USD", "per_second", "input", "", "inventory_mode_unsupported"},
		{"per image", "gemini-4-argon", "image", "USD", "per_image", "input", "", "inventory_mode_unsupported"},
		{"requested 1h cache", "gemini-4-argon", "token", "USD", "per_million_tokens", "cache_write_1h", "", "required_field_unsupported"},
		{"requested tiered input", "gemini-4-argon", "token", "USD", "per_million_tokens", "input", "tiered", "inventory_structure_unsupported"},
		{"source currency", "local-currency", "token", "USD", "per_million_tokens", "input", "", "quote_currency_unsupported"},
		{"source unit", "per-call", "token", "USD", "per_million_tokens", "input", "", "quote_unit_unsupported"},
		{"source tier", "tiered", "token", "USD", "per_million_tokens", "input", "", "quote_structure_unsupported"},
		{"source renamed schema", "old-shape", "token", "USD", "per_million_tokens", "input", "", "quote_structure_unsupported"},
		{"string number", "bad-value", "token", "USD", "per_million_tokens", "input", "", "quote_value_unsupported"},
		{"negative number", "bad-value", "token", "USD", "per_million_tokens", "cache_read", "", "quote_value_unsupported"},
		{"model id mismatch", "bad-alias", "token", "USD", "per_million_tokens", "input", "", "model_id_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := fmt.Sprintf(`{"rows":[{"provider_id":"google","billing_model_id":%q,"mode":%q,"currency":%q,"unit":%q,"required_fields":[%q],"structure":%q}]}`, tc.model, tc.mode, tc.currency, tc.unit, tc.field, tc.structure)
			report, err := PreviewModelsPricingCoverage([]byte(modelsPricingPreviewTestSnapshot), []byte(row))
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Rows[0].Fields[0]; got.Status != "incompatible" || got.Reason != tc.reason || got.Quote != nil {
				t.Errorf("field = %#v, want incompatible with reason %q", got, tc.reason)
			}
		})
	}
}

func TestModelsPricingPreviewFieldIsolation(t *testing.T) {
	const inventory = `{"rows":[
	  {"provider_id":"google","billing_model_id":"bad-value","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input","output"],"explicit_overrides":{"input":"channel"}},
	  {"provider_id":"google","billing_model_id":"tiny","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]},
	  {"provider_id":"google","billing_model_id":"null-cost","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]}
	]}`
	report, err := PreviewModelsPricingCoverage([]byte(modelsPricingPreviewTestSnapshot), []byte(inventory))
	if err != nil {
		t.Fatal(err)
	}
	if row := report.Rows[0]; row.Status != "comparable" || row.OverrideCoverage != "partial" || row.Fields[0].Status != "explicit_override" || row.Fields[1].Status != "comparable" {
		t.Errorf("covered source input prevented an independent output comparison: %#v", row)
	}
	if quote := report.Rows[1].Fields[0].Quote; quote == nil || quote.String() != "1e-400" {
		t.Errorf("raw numeric quote must not round to zero: %v", quote)
	}
	if field := report.Rows[2].Fields[0]; field.Status != "missing" || field.Quote != nil {
		t.Errorf("null price became a quote: %#v", field)
	}
	body, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(body), `"quote":null`) || !strings.Contains(string(body), `"quote":1e-400`) {
		t.Errorf("quote JSON = %s, error = %v", body, err)
	}
}

func TestModelsPricingPreviewRejectsBadInput(t *testing.T) {
	const goodInventory = `{"rows":[{"provider_id":"google","billing_model_id":"gemini-4-argon","mode":"token","currency":"USD","unit":"per_million_tokens","required_fields":["input"]}]}`
	const rowPrefix = `{"rows":[{"provider_id":"google","billing_model_id":"gemini-4-argon","mode":"token","currency":"USD","unit":"per_million_tokens",`
	for _, bad := range []struct{ snapshot, inventory, stage string }{
		{"{", goodInventory, "snapshot"},
		{"{}", goodInventory, "snapshot"},
		{`{"google":{"models":[]}}`, goodInventory, "snapshot"},
		{`{"google":{"models":{}}}`, goodInventory, "snapshot"},
		{modelsPricingPreviewTestSnapshot, `{"rows":[]}`, "inventory"},
		{modelsPricingPreviewTestSnapshot, goodInventory + " {}", "inventory"},
		{modelsPricingPreviewTestSnapshot, rowPrefix + `"required_fields":["input","input"]}]}`, "inventory"},
		{modelsPricingPreviewTestSnapshot, rowPrefix + `"required_fields":["input"],"explicit_overrides":{"output":"group"}}]}`, "inventory"},
		{modelsPricingPreviewTestSnapshot, rowPrefix + `"required_fields":["input"],"explicit_overrides":{"input":"fallback"}}]}`, "inventory"},
		{modelsPricingPreviewTestSnapshot, rowPrefix + `"required_fields":["input"],"old_admin_default":9}]}`, "inventory"},
	} {
		if report, err := PreviewModelsPricingCoverage([]byte(bad.snapshot), []byte(bad.inventory)); report != nil || err == nil || !strings.Contains(err.Error(), bad.stage) {
			t.Errorf("bad %s => report = %#v, error = %v", bad.stage, report, err)
		}
	}
}
