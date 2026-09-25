package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"text-salesforce-route-chat/internal/bootstrap"
	"text-salesforce-route-chat/internal/config"
	"text-salesforce-route-chat/internal/router"
	"text-salesforce-route-chat/internal/salesforce"
	"text-salesforce-route-chat/internal/textapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		exit("invalid configuration", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	text := textapi.New(cfg.TextBasicAuth, cfg.TextConfigurationURL, cfg.TextAgentURL, cfg.TextCDPURL)
	sf := salesforce.New(cfg.SalesforceLoginURL, cfg.SalesforceClientID, cfg.SalesforceSecret, cfg.SalesforcePlanField, cfg.SalesforceAPIVersion)

	slog.Info("checking Text and Salesforce prerequisites")
	startupCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	state, err := bootstrap.Run(startupCtx, text, sf, bootstrap.Options{
		RegularGroupName:    cfg.RegularGroupName,
		EnterpriseGroupName: cfg.EnterpriseGroupName,
		RegularAgentIDs:     cfg.RegularAgentIDs,
		EnterpriseAgentIDs:  cfg.EnterpriseAgentIDs,
	})
	cancel()
	if err != nil {
		exit("startup checks failed", err)
	}

	mux := newMux(
		router.New(text, sf, state, cfg.TextWebhookSecret, slog.Default()),
	)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown failed", "error", err)
		}
	}()

	slog.Info("Salesforce chat routing ready", "port", cfg.Port, "regular_group_id", state.RegularGroupID, "enterprise_group_id", state.EnterpriseGroupID)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		exit("server failed", err)
	}
}

func newMux(webhook http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/text", webhook)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func exit(message string, err error) {
	slog.Error(message, "error", err)
	os.Exit(1)
}
