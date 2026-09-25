package salesforce

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newServerClient(t *testing.T, handler func(http.ResponseWriter, *http.Request, string)) (*Client, *httptest.Server) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/services/oauth2/token" {
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "id" || r.Form.Get("client_secret") != "secret" {
				t.Errorf("unexpected OAuth request: %s %v", r.Method, r.Form)
			}

			json.NewEncoder(w).Encode(map[string]string{"access_token": "token", "instance_url": server.URL})
			return
		}

		handler(w, r, server.URL)
	}))
	client := New(server.URL, "id", "secret", "Plan_Tier__c", "61.0")
	client.httpClient = server.Client()
	return client, server
}

func TestVerifyChecksContactFieldAndPicklistValues(t *testing.T) {
	client, server := newServerClient(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path != "/services/data/v61.0/sobjects/Contact/describe" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected Contact describe request: %s", r.URL)
		}

		fmt.Fprint(w, `{"fields":[{"name":"Email","type":"email"},{"name":"Plan_Tier__c","type":"picklist","picklistValues":[{"value":"Basic","active":true},{"value":"Standard","active":true},{"value":"Enterprise","active":true}]}]}`)
	})
	defer server.Close()

	if err := client.Verify(context.Background()); err != nil {
		t.Fatalf("expected accessible Contact fields: %v", err)
	}
}

func TestVerifyRejectsHiddenField(t *testing.T) {
	client, server := newServerClient(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		fmt.Fprint(w, `{"fields":[{"name":"Email","type":"email"}]}`)
	})
	defer server.Close()

	if err := client.Verify(context.Background()); err == nil || !strings.Contains(err.Error(), "Plan_Tier__c") {
		t.Fatalf("expected an actionable missing field error, got %v", err)
	}
}

func TestVerifyReportsOAuthFailureBeforeContactLookup(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/oauth2/token" {
			t.Errorf("unexpected Contact lookup after OAuth failure: %s", r.URL)
		}
		http.Error(w, "invalid_client", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := New(server.URL, "id", "secret", "Plan_Tier__c", "61.0")
	client.httpClient = server.Client()
	if err := client.Verify(context.Background()); err == nil || !strings.Contains(err.Error(), "authenticate with Salesforce: HTTP 401: invalid_client") {
		t.Fatalf("expected Salesforce OAuth error, got %v", err)
	}
}

func TestPlanByEmailEscapesSOQLAndReadsTier(t *testing.T) {
	client, server := newServerClient(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		query := r.URL.Query().Get("q")
		if r.URL.Path != "/services/data/v61.0/query" || !strings.Contains(query, `WHERE Email = 'o\'hara@example.com' LIMIT 2`) {
			t.Errorf("unexpected Salesforce query: %q", query)
		}

		fmt.Fprint(w, `{"records":[{"Id":"003123","Plan_Tier__c":"Enterprise"}]}`)
	})
	defer server.Close()

	tier, found, err := client.PlanByEmail(context.Background(), "o'hara@example.com")
	if err != nil || !found || tier != "Enterprise" {
		t.Fatalf("expected Enterprise Contact, tier=%q found=%t err=%v", tier, found, err)
	}
}

func TestPlanByEmailHandlesMissingDuplicateAndUnassignedContacts(t *testing.T) {
	client, server := newServerClient(t, func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case strings.Contains(r.URL.Query().Get("q"), "missing@example.com"):
			fmt.Fprint(w, `{"records":[]}`)
		case strings.Contains(r.URL.Query().Get("q"), "duplicate@example.com"):
			fmt.Fprint(w, `{"records":[{"Plan_Tier__c":"Basic"},{"Plan_Tier__c":"Enterprise"}]}`)
		default:
			fmt.Fprint(w, `{"records":[{"Plan_Tier__c":null}]}`)
		}
	})
	defer server.Close()

	if _, found, err := client.PlanByEmail(context.Background(), "missing@example.com"); found || err != nil {
		t.Fatalf("no matching Contact should be a skip, found=%t err=%v", found, err)
	}

	if _, _, err := client.PlanByEmail(context.Background(), "duplicate@example.com"); err != ErrAmbiguousContact {
		t.Fatalf("expected ambiguous Contact skip, got %v", err)
	}

	if tier, found, err := client.PlanByEmail(context.Background(), "no-tier@example.com"); !found || tier != "" || err != nil {
		t.Fatalf("null tier should be a skip, tier=%q found=%t err=%v", tier, found, err)
	}
}

func TestPlanByEmailAuthenticatesEachRequestWithoutRetrying(t *testing.T) {
	var tokens, queries atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/services/oauth2/token" {
			id := tokens.Add(1)
			json.NewEncoder(w).Encode(map[string]string{"access_token": fmt.Sprintf("token-%d", id), "instance_url": server.URL})
			return
		}

		query := queries.Add(1)
		if got, want := r.Header.Get("Authorization"), fmt.Sprintf("Bearer token-%d", query); got != want {
			t.Errorf("unexpected authorization: got %q, want %q", got, want)
		}
		if r.Header.Get("Authorization") == "Bearer token-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		fmt.Fprint(w, `{"records":[{"Plan_Tier__c":"Standard"}]}`)
	}))
	defer server.Close()

	client := New(server.URL, "id", "secret", "Plan_Tier__c", "61.0")
	client.httpClient = server.Client()
	if _, _, err := client.PlanByEmail(context.Background(), "customer@example.com"); err == nil || !strings.Contains(err.Error(), "HTTP 401") || tokens.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("expected first request to fail without retry, err=%v tokens=%d queries=%d", err, tokens.Load(), queries.Load())
	}

	for call := int32(2); call <= 3; call++ {
		tier, found, err := client.PlanByEmail(context.Background(), "customer@example.com")
		if err != nil || !found || tier != "Standard" || tokens.Load() != call || queries.Load() != call {
			t.Fatalf("expected fresh token for request %d, tier=%q found=%t err=%v tokens=%d queries=%d", call, tier, found, err, tokens.Load(), queries.Load())
		}
	}
}
