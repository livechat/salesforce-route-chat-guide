package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMuxMethods(t *testing.T) {
	webhookCalls := 0
	mux := newMux(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		webhookCalls++
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		name, method, path string
		wantStatus         int
		wantAllow          string
		wantWebhookCalls   int
	}{
		{"webhook POST", http.MethodPost, "/webhooks/text", http.StatusNoContent, "", 1},
		{"webhook GET", http.MethodGet, "/webhooks/text", http.StatusMethodNotAllowed, "POST", 1},
		{"webhook HEAD", http.MethodHead, "/webhooks/text", http.StatusMethodNotAllowed, "POST", 1},
		{"health GET", http.MethodGet, "/healthz", http.StatusNoContent, "", 1},
		{"health HEAD", http.MethodHead, "/healthz", http.StatusNoContent, "", 1},
		{"health POST", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "GET, HEAD", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
			if w.Code != test.wantStatus || w.Header().Get("Allow") != test.wantAllow || webhookCalls != test.wantWebhookCalls {
				t.Fatalf("status=%d Allow=%q webhook calls=%d; want %d, %q, %d", w.Code, w.Header().Get("Allow"), webhookCalls, test.wantStatus, test.wantAllow, test.wantWebhookCalls)
			}
		})
	}
}
