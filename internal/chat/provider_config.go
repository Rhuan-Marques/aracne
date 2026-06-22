package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "embed"
)

// go:embed provider_models.json
var providerModelsJSON []byte

var supportedProviderOrder = []ProviderName{ProviderAnthropic, ProviderOpenAI, ProviderDeepSeek}

// Provider default configuration holding the main default provider name.
type ProviderDefaults struct {
	Main string `json:"main,omitempty"`
}

// Groups of provider configurations organized into supported built-in providers and custom user-defined providers.
type ProviderGroups struct {
	Supported map[ProviderName]ProviderConfigEntry `json:"supported"`
	Custom    map[string]ProviderConfigEntry       `json:"custom"`
}

// Top-level LLM provider configuration with defaults and grouped provider settings.
type ProviderConfig struct {
	Defaults  ProviderDefaults `json:"defaults"`
	Providers ProviderGroups   `json:"providers"`
}

// Individual LLM provider entry with API key, base URL, and supported models configuration.
type ProviderConfigEntry struct {
	Key            string       `json:"key,omitempty"`
	KeyEnv         string       `json:"key_env,omitempty"`
	KeyFrom        string       `json:"key_from,omitempty"`
	BaseURL        string       `json:"base_url,omitempty"`
	ChatContract   ProviderName `json:"chat_contract,omitempty"`
	PossibleModels []string     `json:"possible_models,omitempty"`
	KeyConfigured  bool         `json:"key_configured,omitempty"`
}

// LLM model metadata with internal true name and user-facing display name.
type ProviderModel struct {
	TrueName    string `json:"true_name"`
	DisplayName string `json:"display_name"`
}

// Maps an LLM provider's display name to its available models.
type SupportedProviderModels struct {
	DisplayName string          `json:"display_name"`
	Models      []ProviderModel `json:"models"`
}

// Root configuration state containing provider defaults, supported and custom provider configs, and available models per provider.
type ProviderConfigState struct {
	Defaults        ProviderDefaults                         `json:"defaults"`
	Providers       ProviderGroups                           `json:"providers"`
	SupportedModels map[ProviderName]SupportedProviderModels `json:"supported_models"`
}

// JSON-serializable mapping of provider names to their supported models.
type providerModelsFile struct {
	Supported map[ProviderName]SupportedProviderModels `json:"supported"`
}

// Error type wrapping provider contract validation failures.
type ProviderContractError struct {
	Err error
}

// Returns a formatted error message for provider contract violations
func (e *ProviderContractError) Error() string {
	if e == nil || e.Err == nil {
		return "Provider Contract Broken"
	}
	return "Provider Contract Broken: " + e.Err.Error()
}

// Returns the wrapped error for error chain traversal
func (e *ProviderContractError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Reads and unmarshals provider configuration from a JSON file with defaults.
func loadProviderConfig(path string) (ProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalizeProviderConfig(ProviderConfig{}), nil
		}
		return ProviderConfig{}, err
	}
	var cfg ProviderConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProviderConfig{}, err
	}
	return normalizeProviderConfig(cfg), nil
}

// Persists provider configuration to a JSON file with normalized settings.
func saveProviderConfig(path string, cfg ProviderConfig) error {
	cfg = normalizeProviderConfig(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Normalizes provider config and returns public defaults, providers, and supported models.
func providerConfigState(cfg ProviderConfig) ProviderConfigState {
	cfg = normalizeProviderConfig(cfg)
	return ProviderConfigState{
		Defaults:        cfg.Defaults,
		Providers:       publicProviderGroups(cfg.Providers),
		SupportedModels: supportedProviderModels(),
	}
}

// Normalizes provider configuration by deduplicating entries, filtering custom providers, and setting defaults.
func normalizeProviderConfig(cfg ProviderConfig) ProviderConfig {
	if cfg.Providers.Supported == nil {
		cfg.Providers.Supported = map[ProviderName]ProviderConfigEntry{}
	}
	if cfg.Providers.Custom == nil {
		cfg.Providers.Custom = map[string]ProviderConfigEntry{}
	}

	supported := map[ProviderName]ProviderConfigEntry{}
	for _, provider := range supportedProviderOrder {
		entry, ok := cfg.Providers.Supported[provider]
		if !ok {
			continue
		}
		entry = normalizeProviderEntry(entry)
		entry.ChatContract = ""
		entry.PossibleModels = nil
		supported[provider] = entry
	}
	cfg.Providers.Supported = supported

	custom := map[string]ProviderConfigEntry{}
	keys := make([]string, 0, len(cfg.Providers.Custom))
	for key := range cfg.Providers.Custom {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		id := strings.TrimSpace(key)
		if id == "" {
			continue
		}
		entry := normalizeProviderEntry(cfg.Providers.Custom[key])
		if !isSupportedProvider(entry.ChatContract) {
			entry.ChatContract = ProviderOpenAI
		}
		entry.PossibleModels = normalizeStringList(entry.PossibleModels)
		custom[id] = entry
	}
	cfg.Providers.Custom = custom

	cfg.Defaults.Main = strings.TrimSpace(cfg.Defaults.Main)
	if cfg.Defaults.Main != "" && !providerModelExists(cfg, cfg.Defaults.Main) {
		cfg.Defaults.Main = ""
	}
	if cfg.Defaults.Main == "" {
		cfg.Defaults.Main = firstConfiguredModel(cfg)
	}
	return cfg
}

// Trims and normalizes individual provider configuration entry fields.
func normalizeProviderEntry(entry ProviderConfigEntry) ProviderConfigEntry {
	entry.Key = strings.TrimSpace(entry.Key)
	entry.KeyEnv = strings.TrimSpace(entry.KeyEnv)
	entry.BaseURL = strings.TrimSpace(entry.BaseURL)
	entry.ChatContract = ProviderName(strings.TrimSpace(string(entry.ChatContract)))
	entry.KeyConfigured = false
	entry.KeyFrom = ""
	return entry
}

// Deduplicates and trims a string list, removing empty and duplicate values.
func normalizeStringList(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// Creates a public view of provider groups with all API keys sanitized.
func publicProviderGroups(groups ProviderGroups) ProviderGroups {
	result := ProviderGroups{Supported: map[ProviderName]ProviderConfigEntry{}, Custom: map[string]ProviderConfigEntry{}}
	for provider, entry := range groups.Supported {
		result.Supported[provider] = publicProviderEntry(entry)
	}
	for id, entry := range groups.Custom {
		result.Custom[id] = publicProviderEntry(entry)
	}
	return result
}

// Sanitizes a provider entry for public use by clearing the API key but marking if it was configured.
func publicProviderEntry(entry ProviderConfigEntry) ProviderConfigEntry {
	entry.KeyConfigured = entry.Key != ""
	entry.Key = ""
	return entry
}

// Merges provider secrets from a current config into a new config where keys are missing.
func mergeProviderSecrets(next, current ProviderConfig) ProviderConfig {
	if next.Providers.Supported == nil {
		next.Providers.Supported = map[ProviderName]ProviderConfigEntry{}
	}
	if next.Providers.Custom == nil {
		next.Providers.Custom = map[string]ProviderConfigEntry{}
	}
	for provider, entry := range next.Providers.Supported {
		if entry.Key == "" && entry.KeyEnv == "" {
			entry.Key = secretFromConfig(current, entry.KeyFrom, string(provider))
			next.Providers.Supported[provider] = entry
		}
	}
	for id, entry := range next.Providers.Custom {
		if entry.Key == "" && entry.KeyEnv == "" {
			entry.Key = secretFromConfig(current, entry.KeyFrom, id)
			next.Providers.Custom[id] = entry
		}
	}
	return next
}

// secretFromConfig resolves a saved API key, honoring a key_from rename hint
// (so renaming a custom provider carries its key over) before falling back to
// the same id. Defeats the "blank save wipes the key" data-loss path.
func secretFromConfig(current ProviderConfig, keyFrom, sameID string) string {
	for _, id := range []string{strings.TrimSpace(keyFrom), sameID} {
		if id == "" {
			continue
		}
		if src, ok := current.Providers.Custom[id]; ok && src.Key != "" {
			return src.Key
		}
		if src, ok := current.Providers.Supported[ProviderName(id)]; ok && src.Key != "" {
			return src.Key
		}
	}
	return ""
}

// Parses and returns supported LLM models organized by provider from embedded JSON config.
func supportedProviderModels() map[ProviderName]SupportedProviderModels {
	var file providerModelsFile
	if err := json.Unmarshal(providerModelsJSON, &file); err != nil || file.Supported == nil {
		return map[ProviderName]SupportedProviderModels{}
	}
	result := map[ProviderName]SupportedProviderModels{}
	for provider, definition := range file.Supported {
		models := make([]ProviderModel, 0, len(definition.Models))
		for _, model := range definition.Models {
			if strings.TrimSpace(model.TrueName) == "" {
				continue
			}
			if strings.TrimSpace(model.DisplayName) == "" {
				model.DisplayName = model.TrueName
			}
			models = append(models, model)
		}
		definition.Models = models
		result[provider] = definition
	}
	return result
}

// Validates if a provider is in the supported set (OpenAI, Anthropic, DeepSeek).
func isSupportedProvider(provider ProviderName) bool {
	switch provider {
	case ProviderOpenAI, ProviderAnthropic, ProviderDeepSeek:
		return true
	default:
		return false
	}
}

// Checks if a model is configured in the provider config.
func providerModelExists(cfg ProviderConfig, model string) bool {
	_, ok := providerSettingsForModel(cfg, model)
	return ok
}

// Returns the first available model from configured providers, prioritizing supported providers then custom ones.
func firstConfiguredModel(cfg ProviderConfig) string {
	for _, provider := range supportedProviderOrder {
		if _, ok := cfg.Providers.Supported[provider]; !ok {
			continue
		}
		if model := firstSupportedProviderModel(provider); model != "" {
			return model
		}
	}
	keys := make([]string, 0, len(cfg.Providers.Custom))
	for key := range cfg.Providers.Custom {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := cfg.Providers.Custom[key]
		if len(entry.PossibleModels) > 0 {
			return entry.PossibleModels[0]
		}
	}
	return ""
}

// Returns the first model available for a given supported provider.
func firstSupportedProviderModel(provider ProviderName) string {
	definition, ok := supportedProviderModels()[provider]
	if !ok || len(definition.Models) == 0 {
		return ""
	}
	return definition.Models[0].TrueName
}

// Looks up provider settings by matching a model name against supported and custom providers.
func providerSettingsForModel(cfg ProviderConfig, model string) (ProviderSettings, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return ProviderSettings{}, false
	}
	models := supportedProviderModels()
	for _, provider := range supportedProviderOrder {
		entry, ok := cfg.Providers.Supported[provider]
		if !ok {
			continue
		}
		for _, candidate := range models[provider].Models {
			if candidate.TrueName == model {
				return settingsFromProviderEntry(provider, entry, model, false), true
			}
		}
	}
	keys := make([]string, 0, len(cfg.Providers.Custom))
	for key := range cfg.Providers.Custom {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := cfg.Providers.Custom[key]
		for _, candidate := range entry.PossibleModels {
			if candidate == model {
				return settingsFromProviderEntry(ProviderName(key), entry, model, true), true
			}
		}
	}
	return ProviderSettings{}, false
}

// Retrieves provider settings for a named provider, using the first model or specified model.
func providerSettingsForName(cfg ProviderConfig, provider ProviderName, model string) (ProviderSettings, bool) {
	if entry, ok := cfg.Providers.Supported[provider]; ok {
		if strings.TrimSpace(model) == "" {
			model = firstSupportedProviderModel(provider)
		}
		return settingsFromProviderEntry(provider, entry, model, false), true
	}
	if entry, ok := cfg.Providers.Custom[string(provider)]; ok {
		if strings.TrimSpace(model) == "" && len(entry.PossibleModels) > 0 {
			model = entry.PossibleModels[0]
		}
		return settingsFromProviderEntry(provider, entry, model, true), true
	}
	return ProviderSettings{}, false
}

// Converts a provider config entry into provider settings with contract resolution.
func settingsFromProviderEntry(provider ProviderName, entry ProviderConfigEntry, model string, custom bool) ProviderSettings {
	settings := ProviderSettings{
		Provider: provider,
		Model:    strings.TrimSpace(model),
		BaseURL:  entry.BaseURL,
		APIKey:   entry.Key,
		KeyEnv:   entry.KeyEnv,
		Custom:   custom,
	}
	if custom {
		settings.Contract = entry.ChatContract
	} else {
		settings.Contract = provider
	}
	return settings
}
