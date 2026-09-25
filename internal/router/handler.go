package router

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"text-salesforce-route-chat/internal/bootstrap"
	"text-salesforce-route-chat/internal/salesforce"
	"text-salesforce-route-chat/internal/textapi"
)

type Text interface {
	GetCustomer(context.Context, string) (textapi.Customer, error)
	SetCustomerTier(context.Context, string, string, string) error
	SetCustomerVIP(context.Context, string) error
	ChatActive(context.Context, string, string) (bool, error)
	TransferChat(context.Context, string, int) error
}

type Salesforce interface {
	PlanByEmail(context.Context, string) (string, bool, error)
}

type Handler struct {
	text       Text
	salesforce Salesforce
	state      bootstrap.State
	secret     [sha256.Size]byte
	logger     *slog.Logger
}

type webhook struct {
	SecretKey string `json:"secret_key"`
	Action    string `json:"action"`
	Payload   struct {
		Chat struct {
			ID    string `json:"id"`
			Users []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"users"`
			Thread struct {
				ID     string `json:"id"`
				Active *bool  `json:"active"`
			} `json:"thread"`
		} `json:"chat"`
	} `json:"payload"`
}

func New(text Text, salesforce Salesforce, state bootstrap.State, webhookSecret string, logger *slog.Logger) *Handler {
	return &Handler{
		text:       text,
		salesforce: salesforce,
		state:      state,
		secret:     sha256.Sum256([]byte(webhookSecret)),
		logger:     logger,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var event webhook
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&event); err != nil {
		http.Error(w, "invalid webhook body", http.StatusBadRequest)
		return
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "invalid webhook body", http.StatusBadRequest)
		return
	}

	provided := sha256.Sum256([]byte(event.SecretKey))
	if subtle.ConstantTimeCompare(h.secret[:], provided[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if event.Action != "incoming_chat" {
		http.Error(w, "unexpected webhook action", http.StatusBadRequest)
		return
	}

	chat := event.Payload.Chat
	if chat.ID == "" || chat.Thread.ID == "" {
		http.Error(w, "missing chat or thread ID", http.StatusBadRequest)
		return
	}

	var customerID string
	for _, user := range chat.Users {
		if user.Type == "customer" {
			if customerID != "" || user.ID == "" {
				http.Error(w, "chat must contain one customer", http.StatusBadRequest)
				return
			}

			customerID = user.ID
		}
	}

	if customerID == "" {
		http.Error(w, "chat must contain one customer", http.StatusBadRequest)
		return
	}

	if chat.Thread.Active != nil && !*chat.Thread.Active {
		h.logger.Info("skipped inactive Text thread", "chat_id", chat.ID, "thread_id", chat.Thread.ID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := h.process(ctx, chat.ID, chat.Thread.ID, customerID); err != nil {
		h.logger.Error("failed to route Text chat", "chat_id", chat.ID, "thread_id", chat.Thread.ID, "error", err)
		http.Error(w, "chat routing failed", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) process(ctx context.Context, chatID, threadID, customerID string) error {
	customer, err := h.text.GetCustomer(ctx, customerID)
	if err != nil {
		return err
	}

	if strings.TrimSpace(customer.Email) == "" {
		h.logger.Info("skipped Text customer without email", "chat_id", chatID, "thread_id", threadID)
		return nil
	}

	tier, found, err := h.salesforce.PlanByEmail(ctx, customer.Email)
	if errors.Is(err, salesforce.ErrAmbiguousContact) {
		h.logger.Info("skipped ambiguous Salesforce Contact lookup", "chat_id", chatID, "thread_id", threadID)
		return nil
	}
	if err != nil {
		return err
	}

	if !found || tier == "" {
		h.logger.Info("skipped chat without Salesforce plan tier", "chat_id", chatID, "thread_id", threadID)
		return nil
	}

	if err := h.text.SetCustomerTier(ctx, customerID, h.state.PropertyID, tier); err != nil {
		return err
	}

	groupID := 0
	switch tier {
	case "Basic":
		h.logger.Info("Basic customer stays with the AI Agent", "chat_id", chatID, "thread_id", threadID)
		return nil
	case "Standard":
		groupID = h.state.RegularGroupID
	case "Enterprise":
		if err := h.text.SetCustomerVIP(ctx, customerID); err != nil {
			return err
		}

		groupID = h.state.EnterpriseGroupID
	default:
		h.logger.Info("skipped routing for unsupported Salesforce tier", "chat_id", chatID, "thread_id", threadID, "tier", tier)
		return nil
	}

	active, err := h.text.ChatActive(ctx, chatID, threadID)
	if err != nil {
		return err
	}

	if !active {
		h.logger.Info("skipped transfer of inactive Text thread", "chat_id", chatID, "thread_id", threadID)
		return nil
	}

	if err := h.text.TransferChat(ctx, chatID, groupID); err != nil {
		return fmt.Errorf("route %s chat: %w", tier, err)
	}

	h.logger.Info("routed Text chat", "chat_id", chatID, "thread_id", threadID, "tier", tier, "group_id", groupID)
	return nil
}
