package textapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestTextAPIRequestContracts(t *testing.T) {
	want := map[string]map[string]any{
		"/v1/get_customer_properties_definitions": {},
		"/v1/create_customer_property_definition": {"name": "salesforce_plan_tier", "type": "string"},
		"/v1/set_customer_properties_values":      {"customer_id": "customer-1", "properties": map[string]any{"property-1": "Enterprise"}},
		"/v1/update_customer":                     {"customer_id": "customer-1", "relationship_level": "vip"},
		"/action/list_groups":                     {"fields": []any{"agent_priorities"}},
		"/action/create_group":                    {"name": "Enterprise", "agent_priorities": map[string]any{"human@example.com": "normal"}},
		"/action/get_group":                       {"id": float64(19), "fields": []any{"agent_priorities"}},
		"/action/get_customer":                    {"id": "customer-1"},
		"/action/get_chat":                        {"chat_id": "chat-1", "thread_id": "thread-1"},
		"/action/transfer_chat": {
			"id": "chat-1", "target": map[string]any{"type": "group", "ids": []any{float64(19)}},
			"ignore_requester_presence": true, "ignore_agents_availability": true,
		},
	}
	responses := map[string]string{
		"/v1/get_customer_properties_definitions": `{"properties":[{"id":"property-1","name":"salesforce_plan_tier","type":"string"}]}`,
		"/v1/create_customer_property_definition": `{"id":"property-1","name":"salesforce_plan_tier","type":"string"}`,
		"/action/list_groups":                     `[{"id":0,"name":"General","agent_priorities":{"human@example.com":"normal"}}]`,
		"/action/create_group":                    `{"id":19}`,
		"/action/get_group":                       `{"id":19,"name":"Enterprise","agent_priorities":{"human@example.com":"normal"}}`,
		"/action/get_customer":                    `{"id":"customer-1","email":"customer@example.com"}`,
		"/action/get_chat":                        `{"thread":{"id":"thread-1","active":true}}`,
	}
	seen := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Basic encoded" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request headers/method at %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, want[r.URL.Path]) {
			t.Errorf("unexpected body at %s: %v err=%v", r.URL.Path, body, err)
		}
		seen[r.URL.Path] = true
		fmt.Fprint(w, responses[r.URL.Path])
	}))
	defer server.Close()

	client := New("encoded", server.URL, server.URL, server.URL)
	client.httpClient = server.Client()
	ctx := context.Background()
	if _, err := client.ListPropertyDefinitions(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := client.CreatePropertyDefinition(ctx, "salesforce_plan_tier"); err != nil {
		t.Fatal(err)
	}

	if err := client.SetCustomerTier(ctx, "customer-1", "property-1", "Enterprise"); err != nil {
		t.Fatal(err)
	}

	if err := client.SetCustomerVIP(ctx, "customer-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := client.ListGroups(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := client.CreateGroup(ctx, "Enterprise", map[string]string{"human@example.com": "normal"}); err != nil {
		t.Fatal(err)
	}

	if _, err := client.GetGroup(ctx, 19); err != nil {
		t.Fatal(err)
	}

	if _, err := client.GetCustomer(ctx, "customer-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := client.ChatActive(ctx, "chat-1", "thread-1"); err != nil {
		t.Fatal(err)
	}

	if err := client.TransferChat(ctx, "chat-1", 19); err != nil {
		t.Fatal(err)
	}

	if len(seen) != len(want) {
		t.Fatalf("some API operations were never called: seen=%v", seen)
	}
}

func TestCreateGroupPreservesForbiddenStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"type":"authorization","message":"unauthorized"}}`)
	}))
	defer server.Close()

	client := New("encoded", server.URL, server.URL, server.URL)
	client.httpClient = server.Client()
	_, err := client.CreateGroup(context.Background(), "Enterprise", map[string]string{"human@example.com": "normal"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected Text API 403, got %v", err)
	}
}
