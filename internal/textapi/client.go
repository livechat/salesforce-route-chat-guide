package textapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	configurationURL string
	agentURL         string
	cdpURL           string
	basicAuth        string
	httpClient       *http.Client
}

type PropertyDefinition struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	TypeConfiguration struct {
		AllowedValues []string `json:"allowed_values"`
	} `json:"type_configuration"`
}

type Group struct {
	ID              int               `json:"id"`
	Name            string            `json:"name"`
	AgentPriorities map[string]string `json:"agent_priorities"`
}

type Customer struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Text API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func New(basicAuth, configurationURL, agentURL, cdpURL string) *Client {
	return &Client{
		configurationURL: configurationURL,
		agentURL:         agentURL,
		cdpURL:           cdpURL,
		basicAuth:        basicAuth,
		httpClient:       &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *Client) ListPropertyDefinitions(ctx context.Context) ([]PropertyDefinition, error) {
	var response struct {
		Properties []PropertyDefinition `json:"properties"`
	}
	if err := c.post(ctx, c.cdpURL+"/v1/get_customer_properties_definitions", map[string]any{}, &response); err != nil {
		return nil, fmt.Errorf("list CDP customer property definitions: %w", err)
	}

	return response.Properties, nil
}

func (c *Client) CreatePropertyDefinition(ctx context.Context, name string) (PropertyDefinition, error) {
	var property PropertyDefinition
	if err := c.post(ctx, c.cdpURL+"/v1/create_customer_property_definition", map[string]string{
		"name": name,
		"type": "string",
	}, &property); err != nil {
		return PropertyDefinition{}, fmt.Errorf("create CDP customer property %q: %w", name, err)
	}

	return property, nil
}

func (c *Client) SetCustomerTier(ctx context.Context, customerID, propertyID, tier string) error {
	if err := c.post(ctx, c.cdpURL+"/v1/set_customer_properties_values", map[string]any{
		"customer_id": customerID,
		"properties":  map[string]string{propertyID: tier},
	}, nil); err != nil {
		return fmt.Errorf("set CDP customer tier: %w", err)
	}

	return nil
}

func (c *Client) SetCustomerVIP(ctx context.Context, customerID string) error {
	if err := c.post(ctx, c.cdpURL+"/v1/update_customer", map[string]string{
		"customer_id":        customerID,
		"relationship_level": "vip",
	}, nil); err != nil {
		return fmt.Errorf("set CDP customer relationship level to VIP: %w", err)
	}

	return nil
}

func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var groups []Group
	if err := c.post(ctx, c.configurationURL+"/action/list_groups", map[string]any{
		"fields": []string{"agent_priorities"},
	}, &groups); err != nil {
		return nil, fmt.Errorf("list Text groups: %w", err)
	}

	return groups, nil
}

func (c *Client) GetGroup(ctx context.Context, id int) (Group, error) {
	var group Group
	if err := c.post(ctx, c.configurationURL+"/action/get_group", map[string]any{
		"id": id, "fields": []string{"agent_priorities"},
	}, &group); err != nil {
		return Group{}, fmt.Errorf("get Text group %d: %w", id, err)
	}

	return group, nil
}

func (c *Client) CreateGroup(ctx context.Context, name string, agents map[string]string) (int, error) {
	var response struct {
		ID int `json:"id"`
	}
	if err := c.post(ctx, c.configurationURL+"/action/create_group", map[string]any{
		"name": name, "agent_priorities": agents,
	}, &response); err != nil {
		return 0, fmt.Errorf("create Text group %q: %w", name, err)
	}

	if response.ID <= 0 {
		return 0, fmt.Errorf("create Text group %q: response has no group ID", name)
	}

	return response.ID, nil
}

func (c *Client) GetCustomer(ctx context.Context, id string) (Customer, error) {
	var customer Customer
	if err := c.post(ctx, c.agentURL+"/action/get_customer", map[string]string{"id": id}, &customer); err != nil {
		return Customer{}, fmt.Errorf("get Text customer: %w", err)
	}

	return customer, nil
}

func (c *Client) ChatActive(ctx context.Context, chatID, threadID string) (bool, error) {
	var chat struct {
		Thread struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		} `json:"thread"`
	}
	if err := c.post(ctx, c.agentURL+"/action/get_chat", map[string]string{
		"chat_id": chatID, "thread_id": threadID,
	}, &chat); err != nil {
		return false, fmt.Errorf("get Text chat: %w", err)
	}

	if chat.Thread.ID != threadID {
		return false, fmt.Errorf("Text returned an unexpected thread for chat %s", chatID)
	}

	return chat.Thread.Active, nil
}

func (c *Client) TransferChat(ctx context.Context, chatID string, groupID int) error {
	if err := c.post(ctx, c.agentURL+"/action/transfer_chat", map[string]any{
		"id": chatID,
		"target": map[string]any{
			"type": "group", "ids": []int{groupID},
		},
		"ignore_requester_presence":  true,
		"ignore_agents_availability": true,
	}, nil); err != nil {
		return fmt.Errorf("transfer Text chat to group %d: %w", groupID, err)
	}

	return nil
}

func (c *Client) post(ctx context.Context, url string, payload, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Basic "+c.basicAuth)
	req.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call Text API: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Text API response: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &APIError{StatusCode: response.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("decode Text API response: %w", err)
		}
	}

	return nil
}
