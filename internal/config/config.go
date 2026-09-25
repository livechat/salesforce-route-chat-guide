package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	fieldNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	versionPattern   = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

type Config struct {
	Port                 int
	TextBasicAuth        string
	TextWebhookSecret    string
	TextConfigurationURL string
	TextAgentURL         string
	TextCDPURL           string
	RegularGroupName     string
	EnterpriseGroupName  string
	RegularAgentIDs      []string
	EnterpriseAgentIDs   []string
	SalesforceLoginURL   string
	SalesforceClientID   string
	SalesforceSecret     string
	SalesforcePlanField  string
	SalesforceAPIVersion string
}

func Load() (Config, error) {
	port := 8080
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return Config{}, errors.New("PORT must be between 1 and 65535")
		}

		port = parsed
	}

	cfg := Config{
		Port:                 port,
		TextBasicAuth:        os.Getenv("TEXT_BASIC_AUTH"),
		TextWebhookSecret:    os.Getenv("TEXT_WEBHOOK_SECRET"),
		TextConfigurationURL: strings.TrimRight(envOr("TEXT_CONFIGURATION_URL", "https://api.livechatinc.com/v3.6/configuration"), "/"),
		TextAgentURL:         strings.TrimRight(envOr("TEXT_AGENT_URL", "https://api.livechatinc.com/v3.6/agent"), "/"),
		TextCDPURL:           strings.TrimRight(envOr("TEXT_CDP_URL", "https://api.text.com/cdp"), "/"),
		RegularGroupName:     envOr("TEXT_REGULAR_GROUP_NAME", "Regular"),
		EnterpriseGroupName:  envOr("TEXT_ENTERPRISE_GROUP_NAME", "Enterprise"),
		RegularAgentIDs:      splitIDs(os.Getenv("TEXT_REGULAR_AGENT_IDS")),
		EnterpriseAgentIDs:   splitIDs(os.Getenv("TEXT_ENTERPRISE_AGENT_IDS")),
		SalesforceLoginURL:   strings.TrimRight(os.Getenv("SALESFORCE_LOGIN_URL"), "/"),
		SalesforceClientID:   os.Getenv("SALESFORCE_CLIENT_ID"),
		SalesforceSecret:     os.Getenv("SALESFORCE_CLIENT_SECRET"),
		SalesforcePlanField:  os.Getenv("SALESFORCE_PLAN_FIELD"),
		SalesforceAPIVersion: envOr("SALESFORCE_API_VERSION", "61.0"),
	}

	if cfg.TextBasicAuth == "" || cfg.TextWebhookSecret == "" ||
		cfg.SalesforceLoginURL == "" || cfg.SalesforceClientID == "" || cfg.SalesforceSecret == "" || cfg.SalesforcePlanField == "" {
		return Config{}, errors.New("TEXT_BASIC_AUTH, TEXT_WEBHOOK_SECRET, SALESFORCE_LOGIN_URL, SALESFORCE_CLIENT_ID, SALESFORCE_CLIENT_SECRET, and SALESFORCE_PLAN_FIELD are required")
	}

	credential, err := base64.StdEncoding.DecodeString(cfg.TextBasicAuth)
	accountID, token, valid := strings.Cut(string(credential), ":")
	if err != nil || !valid || accountID == "" || token == "" {
		return Config{}, errors.New("TEXT_BASIC_AUTH must be the Base64-encoded account_id:personal_access_token value")
	}

	for key, value := range map[string]string{
		"TEXT_CONFIGURATION_URL": cfg.TextConfigurationURL,
		"TEXT_AGENT_URL":         cfg.TextAgentURL,
		"TEXT_CDP_URL":           cfg.TextCDPURL,
	} {
		if err := requireHTTPSBase(value); err != nil {
			return Config{}, fmt.Errorf("%s: %w", key, err)
		}
	}

	if err := requireHTTPSBase(cfg.SalesforceLoginURL); err != nil {
		return Config{}, fmt.Errorf("SALESFORCE_LOGIN_URL: %w", err)
	}
	loginURL, _ := url.Parse(cfg.SalesforceLoginURL)
	if loginURL.Path != "" {
		return Config{}, errors.New("SALESFORCE_LOGIN_URL must be a Salesforce HTTPS domain without a path")
	}
	if host := strings.ToLower(loginURL.Hostname()); host == "login.salesforce.com" || host == "test.salesforce.com" {
		return Config{}, errors.New("SALESFORCE_LOGIN_URL must be your organization's My Domain URL for the client credentials flow, not login.salesforce.com or test.salesforce.com")
	}

	if !fieldNamePattern.MatchString(cfg.SalesforcePlanField) {
		return Config{}, errors.New("SALESFORCE_PLAN_FIELD must be a Salesforce field API name, such as Plan_Tier__c")
	}

	if !versionPattern.MatchString(cfg.SalesforceAPIVersion) {
		return Config{}, errors.New("SALESFORCE_API_VERSION must look like 61.0")
	}

	if cfg.RegularGroupName == cfg.EnterpriseGroupName {
		return Config{}, errors.New("Regular and Enterprise group names must differ")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func splitIDs(raw string) []string {
	var ids []string
	for id := range strings.SplitSeq(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}

func requireHTTPSBase(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must be a public HTTPS URL without credentials, query, or fragment")
	}

	return nil
}
