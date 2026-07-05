package config

import "testing"

func TestValidateEndpoint(t *testing.T) {
	good := []string{
		"https://timpi.com/search?q={query}",
		"http://localhost:8080/api/search?q=x",
		"https://api.example.com/v1/search",
	}
	for _, e := range good {
		if err := validateEndpoint(e); err != nil {
			t.Errorf("validateEndpoint(%q) unexpected error: %v", e, err)
		}
	}
	bad := []string{
		"ftp://example.com/x",
		"file:///etc/passwd",
		"not a url",
		"/relative/only",
		"javascript:alert(1)",
	}
	for _, e := range bad {
		if err := validateEndpoint(e); err == nil {
			t.Errorf("validateEndpoint(%q) should have failed", e)
		}
	}
}

func TestBrowserModeValidates(t *testing.T) {
	c := Default()
	c.Mode = ModeBrowser
	if err := c.Validate(); err != nil {
		t.Errorf("default browser config should validate: %v", err)
	}
	c.Browser.URL = "ftp://nope"
	if err := c.Validate(); err == nil {
		t.Error("non-http browser URL should fail")
	}
}

func TestAlertsSanitizeClamps(t *testing.T) {
	c := Default()
	c.Alerts.MaxErrorRate = 5 // > 1
	c.Alerts.MaxZeroResultRate = -1
	c.Alerts.WindowQueries = 0
	c.Alerts.CooldownSeconds = 0
	c.Sanitize()
	if c.Alerts.MaxErrorRate != 1 {
		t.Errorf("error rate not clamped to 1: %v", c.Alerts.MaxErrorRate)
	}
	if c.Alerts.MaxZeroResultRate != 0 {
		t.Errorf("negative rate not clamped to 0: %v", c.Alerts.MaxZeroResultRate)
	}
	if c.Alerts.WindowQueries != 20 || c.Alerts.CooldownSeconds != 300 {
		t.Errorf("window/cooldown defaults not applied: %d/%d", c.Alerts.WindowQueries, c.Alerts.CooldownSeconds)
	}
}
