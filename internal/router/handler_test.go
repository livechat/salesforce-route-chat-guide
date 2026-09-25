package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"text-salesforce-route-chat/internal/bootstrap"
	"text-salesforce-route-chat/internal/salesforce"
	"text-salesforce-route-chat/internal/textapi"
)

type fakeText struct {
	email       string
	active      bool
	setErr      error
	vipErr      error
	calls       []string
	writtenTier string
	vip         bool
	groupID     int
}

func (f *fakeText) GetCustomer(_ context.Context, id string) (textapi.Customer, error) {
	f.calls = append(f.calls, "get_customer")
	return textapi.Customer{ID: id, Email: f.email}, nil
}

func (f *fakeText) SetCustomerTier(_ context.Context, customerID, propertyID, tier string) error {
	f.calls = append(f.calls, "set_tier")
	if customerID != "customer-1" || propertyID != "property-1" {
		return errors.New("wrong customer or property ID")
	}

	f.writtenTier = tier
	return f.setErr
}

func (f *fakeText) SetCustomerVIP(_ context.Context, customerID string) error {
	f.calls = append(f.calls, "set_vip")
	if customerID != "customer-1" {
		return errors.New("wrong customer ID")
	}
	if f.vipErr != nil {
		return f.vipErr
	}

	f.vip = true
	return nil
}

func (f *fakeText) ChatActive(_ context.Context, _, _ string) (bool, error) {
	f.calls = append(f.calls, "chat_active")
	return f.active, nil
}

func (f *fakeText) TransferChat(_ context.Context, chatID string, groupID int) error {
	f.calls = append(f.calls, "transfer_chat")
	if chatID != "chat-1" {
		return errors.New("wrong chat ID")
	}

	f.groupID = groupID
	return nil
}

type fakeSalesforce struct {
	tier  string
	found bool
	err   error
	calls int
}

func (f *fakeSalesforce) PlanByEmail(_ context.Context, email string) (string, bool, error) {
	f.calls++
	if email != "customer@example.com" {
		return "", false, errors.New("wrong email")
	}

	return f.tier, f.found, f.err
}

func newHandler(text *fakeText, sf *fakeSalesforce) *Handler {
	return New(text, sf, bootstrap.State{
		PropertyID: "property-1", RegularGroupID: 11, EnterpriseGroupID: 19,
	}, "correct-secret", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func webhookBody(threadID, secret string, active bool) []byte {
	body, _ := json.Marshal(map[string]any{
		"webhook_id": "manually-created-hook", "secret_key": secret, "action": "incoming_chat",
		"payload": map[string]any{
			"chat": map[string]any{
				"id": "chat-1", "users": []map[string]string{{"id": "customer-1", "type": "customer"}},
				"thread": map[string]any{"id": threadID, "active": active},
			},
		},
	})
	return body
}

func deliver(h *Handler, body []byte) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/webhooks/text", bytes.NewReader(body)))
	return w.Code
}

func TestChatTierRoutingWritesCDPBeforeTransfer(t *testing.T) {
	for _, test := range []struct {
		tier      string
		wantCalls []string
		wantGroup int
	}{
		{"Basic", []string{"get_customer", "set_tier"}, 0},
		{"Standard", []string{"get_customer", "set_tier", "chat_active", "transfer_chat"}, 11},
		{"Enterprise", []string{"get_customer", "set_tier", "set_vip", "chat_active", "transfer_chat"}, 19},
	} {
		t.Run(test.tier, func(t *testing.T) {
			text := &fakeText{email: "customer@example.com", active: true}
			sf := &fakeSalesforce{tier: test.tier, found: true}
			if status := deliver(newHandler(text, sf), webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent {
				t.Fatalf("expected 204, got %d", status)
			}

			if text.writtenTier != test.tier || !slices.Equal(text.calls, test.wantCalls) || text.groupID != test.wantGroup || text.vip != (test.tier == "Enterprise") {
				t.Fatalf("tier=%s written=%s calls=%v group=%d vip=%v", test.tier, text.writtenTier, text.calls, text.groupID, text.vip)
			}
		})
	}
}

func TestNoContactOrTierStopsBeforeCDPWrite(t *testing.T) {
	for _, test := range []struct {
		name  string
		email string
		tier  string
		found bool
		err   error
	}{
		{"no email", "", "", false, nil},
		{"no Contact", "customer@example.com", "", false, nil},
		{"no tier", "customer@example.com", "", true, nil},
		{"duplicate Contacts", "customer@example.com", "", false, salesforce.ErrAmbiguousContact},
	} {
		t.Run(test.name, func(t *testing.T) {
			text := &fakeText{email: test.email, active: true}
			sf := &fakeSalesforce{tier: test.tier, found: test.found, err: test.err}
			if status := deliver(newHandler(text, sf), webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent {
				t.Fatalf("expected skipped chat to return 204, got %d", status)
			}

			if !slices.Equal(text.calls, []string{"get_customer"}) {
				t.Fatalf("unexpected Text mutations: %v", text.calls)
			}
		})
	}
}

func TestUnknownTierIsStoredButNotTransferred(t *testing.T) {
	text := &fakeText{email: "customer@example.com", active: true}
	sf := &fakeSalesforce{tier: "Premium", found: true}
	if status := deliver(newHandler(text, sf), webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", status)
	}

	if text.writtenTier != "Premium" || !slices.Equal(text.calls, []string{"get_customer", "set_tier"}) {
		t.Fatalf("unknown Salesforce tier should only be stored: %v", text.calls)
	}
}

func TestCDPFailureDoesNotTransferAndCanBeRetried(t *testing.T) {
	text := &fakeText{email: "customer@example.com", active: true, setErr: errors.New("CDP temporarily unavailable")}
	sf := &fakeSalesforce{tier: "Enterprise", found: true}
	h := newHandler(text, sf)
	body := webhookBody("thread-1", "correct-secret", true)
	if status := deliver(h, body); status != http.StatusServiceUnavailable || !slices.Equal(text.calls, []string{"get_customer", "set_tier"}) {
		t.Fatalf("CDP failure must stop transfer, got status %d calls %v", status, text.calls)
	}

	text.setErr = nil
	if status := deliver(h, body); status != http.StatusNoContent || text.groupID != 19 || sf.calls != 2 {
		t.Fatalf("expected retry to route after CDP recovered, status=%d group=%d lookups=%d", status, text.groupID, sf.calls)
	}
}

func TestVIPFailureDoesNotTransferAndCanBeRetried(t *testing.T) {
	text := &fakeText{email: "customer@example.com", active: true, vipErr: errors.New("CDP temporarily unavailable")}
	sf := &fakeSalesforce{tier: "Enterprise", found: true}
	h := newHandler(text, sf)
	body := webhookBody("thread-1", "correct-secret", true)
	if status := deliver(h, body); status != http.StatusServiceUnavailable || !slices.Equal(text.calls, []string{"get_customer", "set_tier", "set_vip"}) || text.vip {
		t.Fatalf("VIP failure must stop transfer, got status %d calls %v vip %v", status, text.calls, text.vip)
	}

	text.vipErr = nil
	if status := deliver(h, body); status != http.StatusNoContent || text.groupID != 19 || !text.vip || sf.calls != 2 {
		t.Fatalf("expected retry to set VIP and route, status=%d group=%d vip=%v lookups=%d", status, text.groupID, text.vip, sf.calls)
	}
}

func TestNonEnterpriseTiersPreserveExistingVIP(t *testing.T) {
	for _, tier := range []string{"Basic", "Standard", "Premium"} {
		t.Run(tier, func(t *testing.T) {
			text := &fakeText{email: "customer@example.com", active: true, vip: true}
			sf := &fakeSalesforce{tier: tier, found: true}
			if status := deliver(newHandler(text, sf), webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent {
				t.Fatalf("expected 204, got %d", status)
			}

			if !text.vip || slices.Contains(text.calls, "set_vip") {
				t.Fatalf("non-Enterprise tier must leave relationship level untouched: vip=%v calls=%v", text.vip, text.calls)
			}
		})
	}
}

func TestSalesforceFailureDoesNotUseStaleTextTier(t *testing.T) {
	text := &fakeText{email: "customer@example.com", active: true}
	sf := &fakeSalesforce{err: errors.New("Salesforce temporarily unavailable")}
	if status := deliver(newHandler(text, sf), webhookBody("thread-1", "correct-secret", true)); status != http.StatusServiceUnavailable {
		t.Fatalf("expected an upstream error to be retryable, got %d", status)
	}

	if !slices.Equal(text.calls, []string{"get_customer"}) {
		t.Fatalf("should not use an older CDP tier when Salesforce lookup fails: %v", text.calls)
	}
}

func TestExpiredThreadDoesNotTransfer(t *testing.T) {
	text := &fakeText{email: "customer@example.com", active: false}
	sf := &fakeSalesforce{tier: "Standard", found: true}
	if status := deliver(newHandler(text, sf), webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", status)
	}

	if !slices.Equal(text.calls, []string{"get_customer", "set_tier", "chat_active"}) {
		t.Fatalf("inactive chat was transferred: %v", text.calls)
	}
}

func TestWebhookAuthAndIndependentDeliveries(t *testing.T) {
	text := &fakeText{email: "customer@example.com", active: true}
	sf := &fakeSalesforce{tier: "Enterprise", found: true}
	h := newHandler(text, sf)
	if status := deliver(h, webhookBody("thread-1", "bad-secret", true)); status != http.StatusUnauthorized || sf.calls != 0 {
		t.Fatalf("unauthenticated webhook must not look up Salesforce, status=%d", status)
	}

	if status := deliver(h, webhookBody("thread-1", "correct-secret", false)); status != http.StatusNoContent || sf.calls != 0 {
		t.Fatalf("inactive incoming thread must be skipped, status=%d", status)
	}

	if status := deliver(h, webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent || sf.calls != 1 {
		t.Fatalf("active thread should route after an inactive delivery, status=%d lookups=%d", status, sf.calls)
	}

	if status := deliver(h, webhookBody("thread-1", "correct-secret", true)); status != http.StatusNoContent || sf.calls != 2 {
		t.Fatalf("repeated delivery should be processed independently, status=%d lookups=%d", status, sf.calls)
	}
}
