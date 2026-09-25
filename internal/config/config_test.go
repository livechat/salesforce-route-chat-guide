package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func validEnvironment(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"PORT":                       "",
		"TEXT_BASIC_AUTH":            base64.StdEncoding.EncodeToString([]byte("account-id:personal-token")),
		"TEXT_WEBHOOK_SECRET":        "a-long-random-secret",
		"TEXT_CONFIGURATION_URL":     "",
		"TEXT_AGENT_URL":             "",
		"TEXT_CDP_URL":               "",
		"TEXT_REGULAR_GROUP_NAME":    "",
		"TEXT_ENTERPRISE_GROUP_NAME": "",
		"TEXT_REGULAR_AGENT_IDS":     "regular@example.com, second@example.com",
		"TEXT_ENTERPRISE_AGENT_IDS":  "first@example.com, second@example.com",
		"SALESFORCE_LOGIN_URL":       "https://my-domain.my.salesforce.com/",
		"SALESFORCE_CLIENT_ID":       "client-id",
		"SALESFORCE_CLIENT_SECRET":   "client-secret",
		"SALESFORCE_PLAN_FIELD":      "Plan_Tier__c",
		"SALESFORCE_API_VERSION":     "",
	} {
		t.Setenv(key, value)
	}
}

func TestLoadWithoutWebhookManagementCredentials(t *testing.T) {
	validEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.SalesforceLoginURL != "https://my-domain.my.salesforce.com" ||
		cfg.SalesforceAPIVersion != "61.0" || cfg.Port != 8080 || len(cfg.RegularAgentIDs) != 2 || cfg.RegularAgentIDs[0] != "regular@example.com" || len(cfg.EnterpriseAgentIDs) != 2 || cfg.EnterpriseAgentIDs[1] != "second@example.com" || cfg.RegularGroupName != "Regular" || cfg.EnterpriseGroupName != "Enterprise" {
		t.Fatalf("unexpected configuration: %+v", cfg)
	}
	if cfg.TextConfigurationURL != "https://api.livechatinc.com/v3.6/configuration" ||
		cfg.TextAgentURL != "https://api.livechatinc.com/v3.6/agent" ||
		cfg.TextCDPURL != "https://api.text.com/cdp" {
		t.Fatalf("expected production Text API defaults, got configuration=%q agent=%q cdp=%q", cfg.TextConfigurationURL, cfg.TextAgentURL, cfg.TextCDPURL)
	}
}

func TestLoadAcceptsCustomTextAPIURLs(t *testing.T) {
	validEnvironment(t)
	t.Setenv("TEXT_CONFIGURATION_URL", "https://api.example.com/v3.6/configuration/")
	t.Setenv("TEXT_AGENT_URL", "https://api.example.com/v3.6/agent/")
	t.Setenv("TEXT_CDP_URL", "https://cdp.example.com/cdp/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TextConfigurationURL != "https://api.example.com/v3.6/configuration" ||
		cfg.TextAgentURL != "https://api.example.com/v3.6/agent" ||
		cfg.TextCDPURL != "https://cdp.example.com/cdp" {
		t.Fatalf("unexpected Text API URLs: configuration=%q agent=%q cdp=%q", cfg.TextConfigurationURL, cfg.TextAgentURL, cfg.TextCDPURL)
	}
}

func TestLoadRejectsInvalidSalesforceFieldAndLoginURL(t *testing.T) {
	for _, test := range []struct {
		name, key, value, want string
	}{
		{"invalid field", "SALESFORCE_PLAN_FIELD", "Plan_Tier__c FROM Contact", "SALESFORCE_PLAN_FIELD"},
		{"insecure login URL", "SALESFORCE_LOGIN_URL", "http://localhost:8080", "SALESFORCE_LOGIN_URL"},
		{"insecure configuration URL", "TEXT_CONFIGURATION_URL", "http://api.example.com/configuration", "TEXT_CONFIGURATION_URL"},
		{"agent URL with credentials", "TEXT_AGENT_URL", "https://user:password@api.example.com/agent", "TEXT_AGENT_URL"},
		{"CDP URL with query", "TEXT_CDP_URL", "https://api.example.com/cdp?key=value", "TEXT_CDP_URL"},
		{"login URL with path", "SALESFORCE_LOGIN_URL", "https://example.salesforce.com/path", "SALESFORCE_LOGIN_URL"},
		{"shared production login domain", "SALESFORCE_LOGIN_URL", "https://login.salesforce.com", "My Domain"},
		{"shared sandbox login domain", "SALESFORCE_LOGIN_URL", "https://test.salesforce.com", "My Domain"},
		{"invalid Basic credential", "TEXT_BASIC_AUTH", "not-base64", "TEXT_BASIC_AUTH"},
		{"identical group names", "TEXT_REGULAR_GROUP_NAME", "Enterprise", "Regular and Enterprise group names must differ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			validEnvironment(t)
			t.Setenv(test.key, test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadAcceptsCustomRegularGroupName(t *testing.T) {
	validEnvironment(t)
	t.Setenv("TEXT_REGULAR_GROUP_NAME", "Human Support")
	cfg, err := Load()
	if err != nil || cfg.RegularGroupName != "Human Support" {
		t.Fatalf("expected configured Standard destination, name=%q err=%v", cfg.RegularGroupName, err)
	}
}

func TestLoadAcceptsSandboxMyDomain(t *testing.T) {
	validEnvironment(t)
	t.Setenv("SALESFORCE_LOGIN_URL", "https://my-company--demo.sandbox.my.salesforce.com/")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.SalesforceLoginURL != "https://my-company--demo.sandbox.my.salesforce.com" {
		t.Fatalf("unexpected sandbox My Domain: %s", cfg.SalesforceLoginURL)
	}
}
