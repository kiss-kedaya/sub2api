package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	modelsPricingComparable   = "comparable"
	modelsPricingMissing      = "missing"
	modelsPricingIncompatible = "incompatible"
	modelsPricingExplicit     = "explicit_override"
	modelsPricingCurrency     = "USD"
	modelsPricingUnit         = "per_million_tokens"
)

type modelsPricingInventoryRow struct {
	RowID             string            `json:"row_id"`
	ProviderID        string            `json:"provider_id"`
	BillingModelID    string            `json:"billing_model_id"`
	Mode              BillingMode       `json:"mode"`
	Currency          string            `json:"currency"`
	Unit              string            `json:"unit"`
	Structure         string            `json:"structure"`
	RequiredFields    []string          `json:"required_fields"`
	ExplicitOverrides map[string]string `json:"explicit_overrides"`
}

// Reuse the registry's provider and model shapes. Decode models and costs
// separately so a richer price shape is visible to the preview, while the
// metadata catalogue's float pointers would discard fields or reject it.
type modelsPricingSnapshotProvider struct {
	modelsDevProvider
	Models map[string]json.RawMessage `json:"models"`
	modelsPricingQuoteDimensions
}

type modelsPricingSnapshotModel struct {
	modelsDevModel
	Cost    json.RawMessage `json:"cost"`
	Pricing json.RawMessage `json:"pricing"`
	modelsPricingQuoteDimensions
}

type modelsPricingQuoteDimensions struct {
	Currency json.RawMessage `json:"currency"`
	Unit     json.RawMessage `json:"unit"`
}

// ModelsPricingPreviewReport identifies both inputs and reports coverage in
// inventory order. The quotes are source observations for offline review.
type ModelsPricingPreviewReport struct {
	SnapshotSHA256      string                    `json:"snapshot_sha256"`
	InventorySHA256     string                    `json:"inventory_sha256"`
	ComparableQuoteUnit string                    `json:"comparable_quote_unit"`
	Summary             map[string]int            `json:"summary"`
	Rows                []ModelsPricingPreviewRow `json:"rows"`
}

// ModelsPricingPreviewRow is one inventory entry with a one-based input index.
type ModelsPricingPreviewRow struct {
	InputRow         int                         `json:"input_row"`
	RowID            string                      `json:"row_id,omitempty"`
	ProviderID       string                      `json:"provider_id"`
	BillingModelID   string                      `json:"billing_model_id"`
	Mode             BillingMode                 `json:"mode"`
	Currency         string                      `json:"currency"`
	Unit             string                      `json:"unit"`
	Structure        string                      `json:"structure"`
	Status           string                      `json:"status"`
	OverrideCoverage string                      `json:"override_coverage"`
	Fields           []ModelsPricingPreviewField `json:"fields"`
}

// ModelsPricingPreviewField reports one required dimension. Quote is null
// unless the snapshot provides a comparable number, which can be exactly zero.
type ModelsPricingPreviewField struct {
	Field          string       `json:"field"`
	Status         string       `json:"status"`
	Reason         string       `json:"reason,omitempty"`
	OverrideSource string       `json:"override_source,omitempty"`
	Quote          *json.Number `json:"quote"`
}

// PreviewModelsPricingCoverage compares fixed local JSON payloads without
// invoking any catalogue refresh, legacy default lookup or billing service.
func PreviewModelsPricingCoverage(snapshot, inventory []byte) (*ModelsPricingPreviewReport, error) {
	var registry map[string]modelsPricingSnapshotProvider
	if err := json.Unmarshal(snapshot, &registry); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}
	modelCount := 0
	for _, provider := range registry {
		modelCount += len(provider.Models)
	}
	if modelCount == 0 {
		return nil, errors.New("snapshot contains no provider models")
	}
	var input struct {
		Rows []modelsPricingInventoryRow `json:"rows"`
	}
	decoder := json.NewDecoder(bytes.NewReader(inventory))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("parse inventory: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("inventory must contain exactly one JSON document")
	}
	if len(input.Rows) == 0 {
		return nil, errors.New("inventory contains no rows")
	}

	report := &ModelsPricingPreviewReport{
		SnapshotSHA256:      fmt.Sprintf("%x", sha256.Sum256(snapshot)),
		InventorySHA256:     fmt.Sprintf("%x", sha256.Sum256(inventory)),
		ComparableQuoteUnit: modelCatalogPricingUnit,
		Summary:             map[string]int{modelsPricingComparable: 0, modelsPricingMissing: 0, modelsPricingIncompatible: 0, modelsPricingExplicit: 0},
		Rows:                make([]ModelsPricingPreviewRow, 0, len(input.Rows)),
	}
	for i, inputRow := range input.Rows {
		if err := inputRow.validate(); err != nil {
			return nil, fmt.Errorf("inventory row %d: %w", i+1, err)
		}
		row := previewModelsPricingRow(registry, inputRow, i+1)
		report.Rows = append(report.Rows, row)
		report.Summary[row.Status]++
	}
	return report, nil
}

func (r modelsPricingInventoryRow) validate() error {
	if strings.TrimSpace(r.ProviderID) == "" || strings.TrimSpace(r.BillingModelID) == "" || len(r.RequiredFields) == 0 {
		return errors.New("provider_id, billing_model_id and required_fields are required")
	}
	seen := make(map[string]bool, len(r.RequiredFields))
	for _, field := range r.RequiredFields {
		if strings.TrimSpace(field) == "" || seen[field] {
			return fmt.Errorf("invalid or duplicate required field %q", field)
		}
		seen[field] = true
	}
	for field, source := range r.ExplicitOverrides {
		if !seen[field] || (source != PricingSourceGroup && source != PricingSourceChannel) {
			return fmt.Errorf("explicit override for %q must identify a required field and use group or channel", field)
		}
	}
	return nil
}

func previewModelsPricingRow(registry map[string]modelsPricingSnapshotProvider, r modelsPricingInventoryRow, index int) ModelsPricingPreviewRow {
	if r.Structure == "" {
		r.Structure = "flat"
	}
	row := ModelsPricingPreviewRow{
		InputRow: index, RowID: r.RowID, ProviderID: r.ProviderID, BillingModelID: r.BillingModelID,
		Mode: r.Mode, Currency: r.Currency, Unit: r.Unit, Structure: r.Structure,
		Status: modelsPricingComparable, OverrideCoverage: "none", Fields: make([]ModelsPricingPreviewField, 0, len(r.RequiredFields)),
	}
	if len(r.ExplicitOverrides) == len(r.RequiredFields) {
		row.OverrideCoverage = "full"
		row.Status = modelsPricingExplicit
	} else if len(r.ExplicitOverrides) > 0 {
		row.OverrideCoverage = "partial"
	}

	var cost map[string]json.RawMessage
	var status, reason string
	if row.Status != modelsPricingExplicit {
		cost, status, reason = lookupModelsPricingPreviewQuote(registry, r)
	}
	for _, name := range r.RequiredFields {
		field := ModelsPricingPreviewField{Field: name}
		switch source := r.ExplicitOverrides[name]; {
		case source != "":
			field.Status, field.OverrideSource = modelsPricingExplicit, source
		case !isModelsPricingFlatField(name):
			field.Status, field.Reason = modelsPricingIncompatible, "required_field_unsupported"
		case reason != "":
			field.Status, field.Reason = status, reason
		default:
			field.Quote, field.Status, field.Reason = previewModelsPricingValue(cost[name])
		}
		if field.Status == modelsPricingIncompatible || (field.Status == modelsPricingMissing && row.Status == modelsPricingComparable) {
			row.Status = field.Status
		}
		row.Fields = append(row.Fields, field)
	}
	return row
}

func lookupModelsPricingPreviewQuote(registry map[string]modelsPricingSnapshotProvider, r modelsPricingInventoryRow) (map[string]json.RawMessage, string, string) {
	switch {
	case r.Mode != BillingModeToken:
		return nil, modelsPricingIncompatible, "inventory_mode_unsupported"
	case r.Currency != modelsPricingCurrency:
		return nil, modelsPricingIncompatible, "inventory_currency_unsupported"
	case r.Unit != modelsPricingUnit:
		return nil, modelsPricingIncompatible, "inventory_unit_unsupported"
	case r.Structure != "flat":
		return nil, modelsPricingIncompatible, "inventory_structure_unsupported"
	case modelsDevProviderRank(r.ProviderID) != 0:
		return nil, modelsPricingIncompatible, "provider_not_first_party"
	}
	provider, ok := registry[r.ProviderID]
	if !ok {
		return nil, modelsPricingMissing, "provider_missing"
	}
	if provider.ID != "" && provider.ID != r.ProviderID {
		return nil, modelsPricingIncompatible, "provider_id_mismatch"
	}
	modelBody := provider.Models[r.BillingModelID]
	if pricingPreviewJSONMissing(modelBody) {
		return nil, modelsPricingMissing, "model_missing"
	}
	var model modelsPricingSnapshotModel
	if err := json.Unmarshal(modelBody, &model); err != nil {
		return nil, modelsPricingIncompatible, "quote_structure_unsupported"
	}
	if model.ID != "" && model.ID != r.BillingModelID {
		return nil, modelsPricingIncompatible, "model_id_mismatch"
	}
	var cost map[string]json.RawMessage
	if !pricingPreviewJSONMissing(model.Pricing) || (len(model.Cost) > 0 && json.Unmarshal(model.Cost, &cost) != nil) {
		return nil, modelsPricingIncompatible, "quote_structure_unsupported"
	}
	for _, units := range []modelsPricingQuoteDimensions{provider.modelsPricingQuoteDimensions, model.modelsPricingQuoteDimensions, {Currency: cost["currency"], Unit: cost["unit"]}} {
		if len(units.Currency) > 0 && !pricingPreviewStringIs(units.Currency, modelsPricingCurrency) {
			return nil, modelsPricingIncompatible, "quote_currency_unsupported"
		}
		if len(units.Unit) > 0 && !pricingPreviewStringIs(units.Unit, modelsPricingUnit) && !pricingPreviewStringIs(units.Unit, modelCatalogPricingUnit) {
			return nil, modelsPricingIncompatible, "quote_unit_unsupported"
		}
	}
	for key, raw := range cost {
		if key != "currency" && key != "unit" && !isModelsPricingFlatField(key) && !pricingPreviewJSONMissing(raw) {
			return nil, modelsPricingIncompatible, "quote_structure_unsupported"
		}
	}
	return cost, "", ""
}

func isModelsPricingFlatField(name string) bool {
	switch name {
	case "input", "output", "cache_read", "cache_write", "reasoning":
		return true
	default:
		return false
	}
}

func previewModelsPricingValue(raw json.RawMessage) (*json.Number, string, string) {
	if pricingPreviewJSONMissing(raw) {
		return nil, modelsPricingMissing, "price_field_missing"
	}
	// RawMessage contains valid JSON. Only a nonnegative number starts with a
	// digit. Keep its decimal spelling so conversion cannot manufacture zero.
	raw = bytes.TrimSpace(raw)
	if raw[0] < '0' || raw[0] > '9' {
		return nil, modelsPricingIncompatible, "quote_value_unsupported"
	}
	value := json.Number(raw)
	return &value, modelsPricingComparable, ""
}

func pricingPreviewJSONMissing(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func pricingPreviewStringIs(raw json.RawMessage, expected string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == expected
}
