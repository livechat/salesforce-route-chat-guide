package salesforce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrAmbiguousContact = errors.New("multiple Salesforce Contacts have the same email")

type Client struct {
	loginURL   string
	clientID   string
	secret     string
	planField  string
	apiVersion string
	httpClient *http.Client
}

type token struct {
	AccessToken string `json:"access_token"`
	InstanceURL string `json:"instance_url"`
}

func New(loginURL, clientID, secret, planField, apiVersion string) *Client {
	return &Client{
		loginURL:   loginURL,
		clientID:   clientID,
		secret:     secret,
		planField:  planField,
		apiVersion: apiVersion,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *Client) Verify(ctx context.Context) error {
	var response struct {
		Fields []struct {
			Name           string `json:"name"`
			Type           string `json:"type"`
			PicklistValues []struct {
				Value  string `json:"value"`
				Active bool   `json:"active"`
			} `json:"picklistValues"`
		} `json:"fields"`
	}
	if err := c.get(ctx, "/sobjects/Contact/describe", &response); err != nil {
		return fmt.Errorf("verify Salesforce Contact access: %w", err)
	}

	var hasEmail, hasTier bool
	for _, field := range response.Fields {
		if field.Name == "Email" {
			hasEmail = true
		}

		if field.Name != c.planField {
			continue
		}

		hasTier = true
		if field.Type != "string" && field.Type != "picklist" {
			return fmt.Errorf("Salesforce Contact field %s must be text or a single-choice picklist, got %q", c.planField, field.Type)
		}

		if field.Type == "picklist" {
			for _, tier := range []string{"Basic", "Standard", "Enterprise"} {
				found := false
				for _, value := range field.PicklistValues {
					if value.Active && value.Value == tier {
						found = true
						break
					}
				}

				if !found {
					return fmt.Errorf("Salesforce Contact field %s is missing active picklist value %q", c.planField, tier)
				}
			}
		}
	}

	if !hasEmail || !hasTier {
		return fmt.Errorf("Salesforce integration user must be able to read Contact.Email and Contact.%s", c.planField)
	}

	return nil
}

// PlanByEmail returns the plan and whether exactly one Contact matched the email.
func (c *Client) PlanByEmail(ctx context.Context, email string) (string, bool, error) {
	quotedEmail := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(strings.TrimSpace(email))
	query := fmt.Sprintf("SELECT Id, %s FROM Contact WHERE Email = '%s' LIMIT 2", c.planField, quotedEmail)

	var response struct {
		Records []map[string]json.RawMessage `json:"records"`
	}
	if err := c.get(ctx, "/query?q="+url.QueryEscape(query), &response); err != nil {
		return "", false, fmt.Errorf("look up Salesforce Contact: %w", err)
	}

	if len(response.Records) == 0 {
		return "", false, nil
	}

	if len(response.Records) > 1 {
		return "", false, ErrAmbiguousContact
	}

	value, ok := response.Records[0][c.planField]
	if !ok {
		return "", false, fmt.Errorf("Salesforce did not return Contact.%s", c.planField)
	}

	if string(value) == "null" {
		return "", true, nil
	}

	var plan string
	if err := json.Unmarshal(value, &plan); err != nil {
		return "", false, fmt.Errorf("Salesforce Contact.%s is not a string: %w", c.planField, err)
	}

	return strings.TrimSpace(plan), true, nil
}

func (c *Client) get(ctx context.Context, path string, target any) error {
	auth, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, auth.InstanceURL+"/services/data/v"+c.apiVersion+path, nil)
	if err != nil {
		return fmt.Errorf("create Salesforce request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)

	if err := c.do(req, target); err != nil {
		return fmt.Errorf("call Salesforce REST API: %w", err)
	}

	return nil
}

func (c *Client) accessToken(ctx context.Context) (token, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.loginURL+"/services/oauth2/token", strings.NewReader(data.Encode()))
	if err != nil {
		return token{}, fmt.Errorf("create Salesforce OAuth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var auth token
	if err := c.do(req, &auth); err != nil {
		return token{}, fmt.Errorf("authenticate with Salesforce: %w", err)
	}

	auth.InstanceURL = strings.TrimRight(auth.InstanceURL, "/")
	instance, err := url.Parse(auth.InstanceURL)
	if err != nil || instance.Scheme != "https" || instance.Host == "" || instance.User != nil || instance.Path != "" || auth.AccessToken == "" {
		return token{}, errors.New("Salesforce OAuth response is missing an access token or valid HTTPS instance URL")
	}

	return auth, nil
}

func (c *Client) do(req *http.Request, target any) error {
	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
