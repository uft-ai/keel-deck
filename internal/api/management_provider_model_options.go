package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

const (
	maxProviderModelDisplayNameBytes = 256
	maxProviderModelModalities       = 16
	maxProviderModelModalityBytes    = 64
)

// ProviderModelOptionsDTO carries a model entry's settings beyond its name,
// alias, image flag and thinking levels. On a write it is authoritative for
// every field its family has; a model entry sent without it keeps what is stored.
type ProviderModelOptionsDTO struct {
	DisplayName                string   `json:"display_name,omitempty"`
	MaxContextLength           int      `json:"max_context_length,omitempty"`
	ForceMapping               bool     `json:"force_mapping,omitempty"`
	IsCompat                   bool     `json:"is_compat,omitempty"`
	SupportConfigurationUpdate bool     `json:"support_configuration_update,omitempty"`
	InputModalities            []string `json:"input_modalities,omitempty"`
	OutputModalities           []string `json:"output_modalities,omitempty"`
	UseMaxCompletionTokens     bool     `json:"use_max_completion_tokens,omitempty"`
	Operations                 []string `json:"operations,omitempty"`
	Native                     []string `json:"native,omitempty"`
	MaxOutputTokens            int      `json:"max_output_tokens,omitempty"`
	ThinkingMin                int      `json:"thinking_min,omitempty"`
	ThinkingMax                int      `json:"thinking_max,omitempty"`
	ThinkingZeroAllowed        bool     `json:"thinking_zero_allowed,omitempty"`
	ThinkingDynamicAllowed     bool     `json:"thinking_dynamic_allowed,omitempty"`
}

// providerModelOptionFields states which optional settings a family's model
// entry declares in CPA. The display name, force-mapping and the thinking
// bounds exist on every family's entry. CPA decodes each family strictly, so
// one setting written to a family without it makes CPA refuse the whole
// configuration.
type providerModelOptionFields struct {
	hasMaxContextLength    bool
	hasIsCompat            bool
	hasConfigurationUpdate bool
	hasModalities          bool
	hasMaxCompletionTokens bool
	hasOperationMetadata   bool
}

func providerModelOptionFieldsFor(family string) providerModelOptionFields {
	if family == openAICompatibilityFamily {
		return providerModelOptionFields{hasMaxContextLength: true, hasIsCompat: true, hasModalities: true, hasMaxCompletionTokens: true, hasOperationMetadata: true}
	}
	switch management.ConfigKeyFamily(family) {
	case management.ConfigFamilyVertex:
		return providerModelOptionFields{}
	case management.ConfigFamilyCodex, management.ConfigFamilyXAI, management.ConfigFamilyMeta:
		return providerModelOptionFields{hasMaxContextLength: true, hasIsCompat: true, hasConfigurationUpdate: true}
	}
	return providerModelOptionFields{hasMaxContextLength: true, hasIsCompat: true}
}

// providerModelOptionsDTO reports a model's options, or nil when it has none so
// the common plain entry adds nothing to the list response.
func providerModelOptionsDTO(model management.ModelAlias) *ProviderModelOptionsDTO {
	options := ProviderModelOptionsDTO{
		DisplayName:                model.DisplayName,
		MaxContextLength:           model.MaxContextLength,
		ForceMapping:               model.ForceMapping,
		IsCompat:                   model.IsCompat,
		SupportConfigurationUpdate: model.SupportConfigurationUpdate,
		InputModalities:            model.InputModalities,
		OutputModalities:           model.OutputModalities,
		UseMaxCompletionTokens:     model.UseMaxCompletionTokens,
		Operations:                 model.Operations,
		Native:                     model.Native,
		MaxOutputTokens:            model.MaxOutputTokens,
	}
	if model.Thinking != nil {
		options.ThinkingMin = model.Thinking.Min
		options.ThinkingMax = model.Thinking.Max
		options.ThinkingZeroAllowed = model.Thinking.ZeroAllowed
		options.ThinkingDynamicAllowed = model.Thinking.DynamicAllowed
	}
	if options.isZero() {
		return nil
	}
	return &options
}

func (options ProviderModelOptionsDTO) isZero() bool {
	return options.DisplayName == "" && options.MaxContextLength == 0 && !options.ForceMapping &&
		!options.IsCompat && !options.SupportConfigurationUpdate &&
		len(options.InputModalities) == 0 && len(options.OutputModalities) == 0 &&
		!options.UseMaxCompletionTokens && len(options.Operations) == 0 && len(options.Native) == 0 &&
		options.MaxOutputTokens == 0 && options.ThinkingMin == 0 && options.ThinkingMax == 0 &&
		!options.ThinkingZeroAllowed && !options.ThinkingDynamicAllowed
}

// normalizedProviderModelOptions validates the options against the family and
// returns them trimmed. The refusals name the model so a form with many rows
// can point at the one at fault.
func normalizedProviderModelOptions(family, modelName string, options ProviderModelOptionsDTO) (ProviderModelOptionsDTO, error) {
	refuse := func(reason string) (ProviderModelOptionsDTO, error) {
		return ProviderModelOptionsDTO{}, newProviderWriteError(http.StatusBadRequest, fmt.Sprintf("model %q: %s", modelName, reason))
	}
	fields := providerModelOptionFieldsFor(family)
	switch {
	case !fields.hasMaxContextLength && options.MaxContextLength != 0:
		return refuse("this provider family has no max_context_length")
	case !fields.hasIsCompat && options.IsCompat:
		return refuse("this provider family has no is_compat")
	case !fields.hasConfigurationUpdate && options.SupportConfigurationUpdate:
		return refuse("this provider family has no support_configuration_update")
	case !fields.hasModalities && (len(options.InputModalities) > 0 || len(options.OutputModalities) > 0):
		return refuse("this provider family has no modalities")
	case !fields.hasMaxCompletionTokens && options.UseMaxCompletionTokens:
		return refuse("this provider family has no use_max_completion_tokens")
	case !fields.hasOperationMetadata && (len(options.Operations) > 0 || len(options.Native) > 0 || options.MaxOutputTokens != 0):
		return refuse("this provider family has no operation metadata")
	}
	if fields.hasOperationMetadata {
		var err error
		if options.Operations, err = normalizedOperationList(options.Operations, false); err != nil {
			return refuse("operations: " + err.Error())
		}
		if options.Native, err = normalizedOperationList(options.Native, true); err != nil {
			return refuse("native: " + err.Error())
		}
		if options.MaxOutputTokens < 0 {
			return refuse("max_output_tokens must not be negative")
		}
		for _, operation := range options.Native {
			if !containsOperation(options.Operations, operation) && options.Operations != nil {
				return refuse(fmt.Sprintf("native operation %q is not listed in operations", operation))
			}
		}
	}

	options.DisplayName = strings.TrimSpace(options.DisplayName)
	if len(options.DisplayName) > maxProviderModelDisplayNameBytes {
		return refuse("display_name is too long")
	}
	if options.MaxContextLength < 0 {
		return refuse("max_context_length must not be negative")
	}
	if options.ThinkingMin < 0 || options.ThinkingMax < 0 {
		return refuse("thinking bounds must not be negative")
	}
	if options.ThinkingMax > 0 && options.ThinkingMin > options.ThinkingMax {
		return refuse("thinking_min must not exceed thinking_max")
	}
	var err error
	if options.InputModalities, err = normalizedModalities(options.InputModalities); err != nil {
		return refuse("input_modalities: " + err.Error())
	}
	if options.OutputModalities, err = normalizedModalities(options.OutputModalities); err != nil {
		return refuse("output_modalities: " + err.Error())
	}
	return options, nil
}

var openAICompatOperationNames = management.OpenAICompatOperationNames

func normalizedOperationList(values []string, nativeOnly bool) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("must not be empty; omit it to keep the default")
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		allowed := containsOperation(openAICompatOperationNames, value)
		if nativeOnly {
			allowed = value == "responses" || value == "completions"
		}
		if !allowed {
			return nil, fmt.Errorf("%q is not a supported operation", value)
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("must not be empty; omit it to keep the default")
	}
	return normalized, nil
}

func containsOperation(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func storedModelKey(name, alias string) string {
	name = strings.TrimSpace(name)
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		alias = strings.ToLower(name)
	}
	return name + "\x00" + alias
}

// normalizedModalities lowercases and deduplicates, which is how CPA compares them.
func normalizedModalities(modalities []string) ([]string, error) {
	if len(modalities) > maxProviderModelModalities {
		return nil, fmt.Errorf("at most %d entries", maxProviderModelModalities)
	}
	normalized := make([]string, 0, len(modalities))
	seen := make(map[string]bool, len(modalities))
	for _, modality := range modalities {
		modality = strings.ToLower(strings.TrimSpace(modality))
		if modality == "" || seen[modality] {
			continue
		}
		if len(modality) > maxProviderModelModalityBytes || strings.ContainsAny(modality, ", \t\r\n") {
			return nil, fmt.Errorf("%q is not a modality name", modality)
		}
		seen[modality] = true
		normalized = append(normalized, modality)
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

// applyTo writes the options onto a model, leaving its name, alias, image flag
// and thinking levels alone. An entry whose thinking ends with neither bounds
// nor levels loses the object, as CPA reads an empty one as "no thinking".
func (options ProviderModelOptionsDTO) applyTo(model *management.ModelAlias) {
	model.DisplayName = options.DisplayName
	model.MaxContextLength = options.MaxContextLength
	model.ForceMapping = options.ForceMapping
	model.IsCompat = options.IsCompat
	model.SupportConfigurationUpdate = options.SupportConfigurationUpdate
	model.InputModalities = options.InputModalities
	model.OutputModalities = options.OutputModalities
	model.UseMaxCompletionTokens = options.UseMaxCompletionTokens
	model.Operations = options.Operations
	model.Native = options.Native
	model.MaxOutputTokens = options.MaxOutputTokens

	thinking := management.ThinkingSupport{}
	if model.Thinking != nil {
		thinking.Levels = model.Thinking.Levels
	}
	thinking.Min = options.ThinkingMin
	thinking.Max = options.ThinkingMax
	thinking.ZeroAllowed = options.ThinkingZeroAllowed
	thinking.DynamicAllowed = options.ThinkingDynamicAllowed
	model.Thinking = compactThinking(thinking)
}

func compactThinking(thinking management.ThinkingSupport) *management.ThinkingSupport {
	if thinking.Min == 0 && thinking.Max == 0 && !thinking.ZeroAllowed && !thinking.DynamicAllowed && len(thinking.Levels) == 0 {
		return nil
	}
	return &thinking
}

// editedProviderModel is one model row of a save: the model as the form states
// it, and whether the form stated its options too.
type editedProviderModel struct {
	model      management.ModelAlias
	hasOptions bool
	// disabled is nil when the row does not state the switch.
	disabled      *bool
	originalName  string
	originalAlias string
}

// editedProviderModels reads the request's model rows. A row without a name is
// dropped, and an alias left empty repeats the name.
func editedProviderModels(family string, req SaveProviderRequest) ([]editedProviderModel, error) {
	edited := make([]editedProviderModel, 0, len(req.ModelEntries)+len(req.Models))
	if len(req.ModelEntries) == 0 {
		for _, name := range req.Models {
			if name = strings.TrimSpace(name); name != "" {
				edited = append(edited, editedProviderModel{model: management.ModelAlias{Name: name, Alias: name}})
			}
		}
		return edited, nil
	}
	for _, entry := range req.ModelEntries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		alias := strings.TrimSpace(entry.Alias)
		if alias == "" {
			alias = name
		}
		model := management.ModelAlias{Name: name, Alias: alias, Image: entry.Image}
		if entry.Thinking != nil && len(entry.Thinking.Levels) > 0 {
			model.Thinking = &management.ThinkingSupport{Levels: entry.Thinking.Levels}
		}
		if entry.Options != nil {
			options, err := normalizedProviderModelOptions(family, name, *entry.Options)
			if err != nil {
				return nil, err
			}
			options.applyTo(&model)
		}
		if entry.Disabled != nil && *entry.Disabled && family != openAICompatibilityFamily {
			return nil, newProviderWriteError(http.StatusBadRequest, fmt.Sprintf("model %q: only OpenAI-compatible models can be disabled", name))
		}
		edited = append(edited, editedProviderModel{
			model: model, hasOptions: entry.Options != nil, disabled: entry.Disabled,
			originalName: strings.TrimSpace(entry.OriginalName), originalAlias: strings.TrimSpace(entry.OriginalAlias),
		})
	}
	return edited, nil
}

func newProviderModels(edited []editedProviderModel) []management.ModelAlias {
	models := make([]management.ModelAlias, 0, len(edited))
	for _, row := range edited {
		row.model.Disabled = row.disabled != nil && *row.disabled
		models = append(models, row.model)
	}
	return models
}

// mergeModelEdits applies the form's models to the stored ones. A row always
// states the name, alias, image flag and thinking levels. With options it states
// the whole entry CPA models; without them (the agent's provider operations, an
// older client) the stored model of the same name keeps its other settings.
// Either way the fields this console does not model survive. Two routes that
// share an upstream name stay separate: the match is the upstream name plus
// the client alias, with a single same-name row as a fallback for an edit
// that names the stored route in original_name and original_alias. A unique
// upstream name can still match an older client that omits that identity.
// Original identity is only the lookup key: once a row is found, the requested
// name replaces the stored upstream name. An original identity that names no
// stored route is refused rather than appended as a new model.
func mergeModelEdits(stored []management.ModelAlias, edited []editedProviderModel) ([]management.ModelAlias, error) {
	byRoute := make(map[string]management.ModelAlias, len(stored))
	byName := make(map[string]management.ModelAlias, len(stored))
	nameCount := make(map[string]int, len(stored))
	for _, model := range stored {
		byRoute[storedModelKey(model.Name, model.Alias)] = model
		name := strings.TrimSpace(model.Name)
		nameCount[name]++
		if _, seen := byName[name]; !seen {
			byName[name] = model
		}
	}
	merged := make([]management.ModelAlias, 0, len(edited))
	for _, row := range edited {
		keyName := row.originalName
		keyAlias := row.originalAlias
		if keyName == "" {
			keyName = row.model.Name
			keyAlias = row.model.Alias
		}
		old, found := byRoute[storedModelKey(keyName, keyAlias)]
		if !found && row.originalName == "" && nameCount[strings.TrimSpace(row.model.Name)] == 1 {
			old, found = byName[strings.TrimSpace(row.model.Name)]
		}
		if !found {
			if row.originalName != "" {
				return nil, newProviderWriteError(http.StatusBadRequest, fmt.Sprintf("model %q is not a stored route of this provider", row.originalName))
			}
			row.model.Disabled = row.disabled != nil && *row.disabled
			merged = append(merged, row.model)
			continue
		}
		// A row that does not state the switch keeps the stored choice, so an
		// older client's edit cannot turn a disabled model back on.
		disabled := old.Disabled
		if row.disabled != nil {
			disabled = *row.disabled
		}
		if row.hasOptions {
			model := row.model.WithUnmodelledFieldsOf(old)
			model.Disabled = disabled
			merged = append(merged, model)
			continue
		}
		old.Disabled = disabled
		old.Name = row.model.Name
		old.Alias = row.model.Alias
		old.Image = row.model.Image
		thinking := management.ThinkingSupport{}
		if old.Thinking != nil {
			thinking = *old.Thinking
		}
		thinking.Levels = nil
		if row.model.Thinking != nil {
			thinking.Levels = row.model.Thinking.Levels
		}
		old.Thinking = compactThinking(thinking)
		merged = append(merged, old)
	}
	return merged, nil
}
