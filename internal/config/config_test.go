package config

import "testing"

func TestSanitizeEnforcesFloor(t *testing.T) {
	c := Default()
	c.PollSeconds = 3 // below floor
	c.Sanitize()
	if c.PollSeconds != MinPollSeconds {
		t.Fatalf("PollSeconds = %d, want %d (floor)", c.PollSeconds, MinPollSeconds)
	}

	c.PollSeconds = 0
	c.Sanitize()
	if c.PollSeconds < MinPollSeconds {
		t.Fatalf("zero PollSeconds not clamped: %d", c.PollSeconds)
	}
}

func TestSanitizeFillsAndValidates(t *testing.T) {
	var c Config // all zero values
	c.Sanitize()
	if c.Mode != ModeDryRun {
		t.Errorf("empty mode not defaulted to dry-run: %q", c.Mode)
	}
	if c.Generation.Source != SourceBuiltin {
		t.Errorf("empty source not defaulted: %q", c.Generation.Source)
	}
	if c.Generation.LLM.Provider != LLMOllama {
		t.Errorf("empty provider not defaulted: %q", c.Generation.LLM.Provider)
	}
	if c.JitterSeconds < 0 {
		t.Errorf("negative jitter not clamped")
	}
	if c.Logging.Dir == "" {
		t.Errorf("log dir not defaulted")
	}
	if err := c.Validate(); err != nil {
		t.Errorf("dry-run config should validate: %v", err)
	}
}

func TestValidateRequiresEndpointsAndKeys(t *testing.T) {
	c := Default()
	c.Mode = ModePublicWeb
	c.PublicWeb.Endpoint = ""
	if err := c.Validate(); err == nil {
		t.Error("public-web with no endpoint should fail validation")
	}

	c = Default()
	c.Mode = ModeOfficialAPI
	c.API.Endpoint = "https://x/search?q={query}"
	c.API.Key = ""
	if err := c.Validate(); err == nil {
		t.Error("official-api with no key should fail validation")
	}
}

func TestLLMKindsDefaultQuestions(t *testing.T) {
	c := Default()
	c.Generation.LLM.Enabled = true
	c.Generation.LLM.Kinds = LLMKinds{} // none selected
	c.Sanitize()
	if !c.Generation.LLM.Kinds.Questions {
		t.Error("enabling the model with no kinds should default to questions")
	}
}
