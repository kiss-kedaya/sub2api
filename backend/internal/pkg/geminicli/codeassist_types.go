package geminicli

// LoadCodeAssistRequest matches done-hub's internal Code Assist call.
type LoadCodeAssistRequest struct {
	Metadata LoadCodeAssistMetadata `json:"metadata"`
}

type LoadCodeAssistMetadata struct {
	IDEType    string `json:"ideType"`
	Platform   string `json:"platform"`
	PluginType string `json:"pluginType"`
}

type LoadCodeAssistResponse struct {
	CurrentTier             string        `json:"currentTier,omitempty"`
	CloudAICompanionProject string        `json:"cloudaicompanionProject,omitempty"`
	AllowedTiers            []AllowedTier `json:"allowedTiers,omitempty"`
}

type AllowedTier struct {
	ID        string `json:"id"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

type OnboardUserRequest struct {
	TierID   string                 `json:"tierId"`
	Metadata LoadCodeAssistMetadata `json:"metadata"`
}

type OnboardUserResponse struct {
	Done     bool                   `json:"done"`
	Response *OnboardUserResultData `json:"response,omitempty"`
	Name     string                 `json:"name,omitempty"`
}

type OnboardUserResultData struct {
	CloudAICompanionProject any `json:"cloudaicompanionProject,omitempty"`
}
