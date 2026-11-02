package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func validSystemOneDecision() DecisionConfig {
	return DecisionConfig{
		Enabled:  true,
		Provider: DecisionProviderSystemOne,
		SystemOne: SystemOneConfig{
			APIBaseURL:    "https://api.typesafe.ai/v1",
			APIKey:        "k",
			Model:         "jev-latest",
			Timeout:       10,
			RetryCount:    2,
			Threshold:     0.7,
			Instructions:  "Should Ezy reply to current_message?",
			MaxStateBytes: 48000,
		},
	}
}

func decisionErrs(d DecisionConfig) []string {
	cfg := &Config{Decision: d}
	errs := []string{}
	validateDecision(cfg, &errs)
	return errs
}

func TestValidateDecision_SystemOneValid(t *testing.T) {
	d := validSystemOneDecision()
	if errs := decisionErrs(d); len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	d.SystemOne.CriteriaTrue, d.SystemOne.CriteriaFalse = "yes means", "no means"
	if errs := decisionErrs(d); len(errs) != 0 {
		t.Fatalf("expected no errors with criteria, got %v", errs)
	}
}

func TestValidateDecision_SystemOneDoesNotRequireLLMFields(t *testing.T) {
	// LLM fields (system_prompt, max_tokens, ...) are empty here on purpose.
	if errs := decisionErrs(validSystemOneDecision()); len(errs) != 0 {
		t.Fatalf("system_one must not require llm fields, got %v", errs)
	}
}

func TestValidateDecision_SystemOneInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SystemOneConfig)
		want   string
	}{
		{"missing base url", func(s *SystemOneConfig) { s.APIBaseURL = "" }, "system_one.api_base_url is required"},
		{"missing key", func(s *SystemOneConfig) { s.APIKey = "" }, "system_one.api_key is required"},
		{"missing model", func(s *SystemOneConfig) { s.Model = "" }, "system_one.model is required"},
		{"blank instructions", func(s *SystemOneConfig) { s.Instructions = "  " }, "system_one.instructions is required"},
		{"zero timeout", func(s *SystemOneConfig) { s.Timeout = 0 }, "system_one.timeout must be greater than 0"},
		{"negative retry", func(s *SystemOneConfig) { s.RetryCount = -1 }, "system_one.retry_count"},
		{"zero threshold", func(s *SystemOneConfig) { s.Threshold = 0 }, "system_one.threshold"},
		{"threshold above 1", func(s *SystemOneConfig) { s.Threshold = 1.01 }, "system_one.threshold"},
		{"only criteria_true", func(s *SystemOneConfig) { s.CriteriaTrue = "x" }, "set together"},
		{"only criteria_false", func(s *SystemOneConfig) { s.CriteriaFalse = "x" }, "set together"},
		{"zero state budget", func(s *SystemOneConfig) { s.MaxStateBytes = 0 }, "system_one.max_state_bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validSystemOneDecision()
			tc.mutate(&d.SystemOne)
			errs := decisionErrs(d)
			if len(errs) != 1 || !strings.Contains(errs[0], tc.want) {
				t.Fatalf("expected one error containing %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestValidateDecision_ThresholdOneAllowed(t *testing.T) {
	d := validSystemOneDecision()
	d.SystemOne.Threshold = 1
	if errs := decisionErrs(d); len(errs) != 0 {
		t.Fatalf("threshold 1 should be valid, got %v", errs)
	}
}

func TestValidateDecision_UnknownProvider(t *testing.T) {
	d := DecisionConfig{Enabled: false, Provider: "jev"}
	errs := decisionErrs(d)
	if len(errs) != 1 || !strings.Contains(errs[0], `core.decision.provider "jev" is invalid`) {
		t.Fatalf("expected invalid provider error even when disabled, got %v", errs)
	}
}

func TestValidateDecision_AbsentProviderIsLLM(t *testing.T) {
	d := DecisionConfig{Enabled: true}
	if d.EffectiveProvider() != DecisionProviderLLM {
		t.Fatalf("expected llm default, got %q", d.EffectiveProvider())
	}
	errs := strings.Join(decisionErrs(d), "\n")
	if !strings.Contains(errs, "core.decision.system_prompt is required") {
		t.Fatalf("absent provider must validate llm fields, got %q", errs)
	}
	if strings.Contains(errs, "system_one") {
		t.Fatalf("absent provider must not validate system_one fields, got %q", errs)
	}
}

func TestValidateDecision_LLMIgnoresSystemOneBlock(t *testing.T) {
	d := DecisionConfig{
		Enabled:      true,
		Provider:     DecisionProviderLLM,
		LLMConfig:    LLMConfig{Model: "m", APIBaseURL: "u", APIKey: "k", MaxTokens: 1, Timeout: 1},
		SystemPrompt: "sp",
		SystemOne:    SystemOneConfig{Threshold: 5}, // invalid but unused
	}
	if errs := decisionErrs(d); len(errs) != 0 {
		t.Fatalf("llm provider must ignore system_one block, got %v", errs)
	}
}

func TestDecisionRequestBudget(t *testing.T) {
	d := DecisionConfig{LLMConfig: LLMConfig{Timeout: 10, RetryCount: 3}, SystemOne: SystemOneConfig{Timeout: 4, RetryCount: 1}}
	if to, rc := d.RequestBudget(); to != 10 || rc != 3 {
		t.Fatalf("llm budget = %d,%d", to, rc)
	}
	d.Provider = DecisionProviderSystemOne
	if to, rc := d.RequestBudget(); to != 4 || rc != 1 {
		t.Fatalf("system_one budget = %d,%d", to, rc)
	}
}

func TestDecisionConfig_SystemOneKeysUnmarshal(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	yamlText := `
enabled: true
provider: system_one
system_one:
  api_base_url: "https://api.typesafe.ai/v1"
  api_key: "k"
  model: "jev-latest"
  timeout: 8
  retry_count: 2
  threshold: 0.65
  instructions: "Should Ezy reply?"
  criteria_true: "addressed to Ezy"
  criteria_false: "not for Ezy"
  max_state_bytes: 48000
`
	if err := v.ReadConfig(strings.NewReader(yamlText)); err != nil {
		t.Fatal(err)
	}
	var d DecisionConfig
	if err := v.Unmarshal(&d); err != nil {
		t.Fatal(err)
	}
	want := SystemOneConfig{
		APIBaseURL: "https://api.typesafe.ai/v1", APIKey: "k", Model: "jev-latest",
		Timeout: 8, RetryCount: 2, Threshold: 0.65, Instructions: "Should Ezy reply?",
		CriteriaTrue: "addressed to Ezy", CriteriaFalse: "not for Ezy", MaxStateBytes: 48000,
	}
	if d.Provider != DecisionProviderSystemOne || d.SystemOne != want {
		t.Fatalf("unexpected decode: provider=%q system_one=%+v", d.Provider, d.SystemOne)
	}
	if errs := decisionErrs(d); len(errs) != 0 {
		t.Fatalf("decoded config should validate, got %v", errs)
	}
}
