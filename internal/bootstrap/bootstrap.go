package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"text-salesforce-route-chat/internal/textapi"
)

const propertyName = "salesforce_plan_tier"

type Text interface {
	ListPropertyDefinitions(context.Context) ([]textapi.PropertyDefinition, error)
	CreatePropertyDefinition(context.Context, string) (textapi.PropertyDefinition, error)
	ListGroups(context.Context) ([]textapi.Group, error)
	GetGroup(context.Context, int) (textapi.Group, error)
	CreateGroup(context.Context, string, map[string]string) (int, error)
}

type Salesforce interface {
	Verify(context.Context) error
}

type Options struct {
	RegularGroupName    string
	EnterpriseGroupName string
	RegularAgentIDs     []string
	EnterpriseAgentIDs  []string
}

type State struct {
	PropertyID        string
	RegularGroupID    int
	EnterpriseGroupID int
}

func Run(ctx context.Context, text Text, salesforce Salesforce, options Options) (State, error) {
	propertyID, err := ensureProperty(ctx, text)
	if err != nil {
		return State{}, err
	}

	regularID, enterpriseID, err := ensureGroups(ctx, text, options)
	if err != nil {
		return State{}, err
	}

	if err := salesforce.Verify(ctx); err != nil {
		return State{}, fmt.Errorf("verify Salesforce setup: %w", err)
	}

	return State{
		PropertyID: propertyID, RegularGroupID: regularID,
		EnterpriseGroupID: enterpriseID,
	}, nil
}

func ensureProperty(ctx context.Context, text Text) (string, error) {
	properties, err := text.ListPropertyDefinitions(ctx)
	if err != nil {
		return "", err
	}

	var matching []textapi.PropertyDefinition
	for _, property := range properties {
		if property.Name == propertyName {
			matching = append(matching, property)
		}
	}

	if len(matching) > 1 {
		return "", fmt.Errorf("more than one CDP customer property is named %q", propertyName)
	}

	if len(matching) == 0 {
		created, err := text.CreatePropertyDefinition(ctx, propertyName)
		if err != nil {
			return "", err
		}

		matching = append(matching, created)
	}

	property := matching[0]
	if property.ID == "" || property.Type != "string" {
		return "", fmt.Errorf("CDP customer property %q must have type string and a valid ID; found type %q", propertyName, property.Type)
	}

	if len(property.TypeConfiguration.AllowedValues) > 0 {
		return "", fmt.Errorf("CDP customer property %q must not restrict allowed values, so unrecognized Salesforce tiers can still be stored", propertyName)
	}

	return property.ID, nil
}

func ensureGroups(ctx context.Context, text Text, options Options) (int, int, error) {
	groups, err := text.ListGroups(ctx)
	if err != nil {
		return 0, 0, err
	}

	var regular, enterprise *textapi.Group
	for i := range groups {
		if groups[i].Name == options.RegularGroupName {
			if regular != nil {
				return 0, 0, fmt.Errorf("multiple Text groups are named %q", options.RegularGroupName)
			}

			regular = &groups[i]
		}

		if groups[i].Name == options.EnterpriseGroupName {
			if enterprise != nil {
				return 0, 0, fmt.Errorf("multiple Text groups are named %q", options.EnterpriseGroupName)
			}

			enterprise = &groups[i]
		}
	}

	if regular == nil && len(options.RegularAgentIDs) == 0 {
		return 0, 0, fmt.Errorf("TEXT_REGULAR_AGENT_IDS is required to create Text group %q", options.RegularGroupName)
	}
	if enterprise == nil && len(options.EnterpriseAgentIDs) == 0 {
		return 0, 0, fmt.Errorf("TEXT_ENTERPRISE_AGENT_IDS is required to create Text group %q", options.EnterpriseGroupName)
	}
	for _, group := range []*textapi.Group{regular, enterprise} {
		if group != nil && group.ID <= 0 {
			return 0, 0, fmt.Errorf("Text group %q has ID %d; routing groups must not be the default group", group.Name, group.ID)
		}
	}

	regularID, err := ensureGroup(ctx, text, regular, options.RegularGroupName, options.RegularAgentIDs)
	if err != nil {
		return 0, 0, err
	}
	enterpriseID, err := ensureGroup(ctx, text, enterprise, options.EnterpriseGroupName, options.EnterpriseAgentIDs)
	if err != nil {
		return 0, 0, err
	}

	return regularID, enterpriseID, nil
}

func ensureGroup(ctx context.Context, text Text, existing *textapi.Group, name string, requiredIDs []string) (int, error) {
	if existing != nil {
		if err := verifyAgents(*existing, requiredIDs); err != nil {
			return 0, err
		}
		return existing.ID, nil
	}

	agents := make(map[string]string, len(requiredIDs))
	for _, id := range requiredIDs {
		agents[id] = "normal"
	}

	id, err := text.CreateGroup(ctx, name, agents)
	if err != nil {
		var apiErr *textapi.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return 0, fmt.Errorf("Text denied creating group %q: verify that the Text PAT has groups--all:rw and belongs to an administrator, or create the group with eligible agents in the Text app and restart: %w", name, err)
		}
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("Text group %q was created with invalid ID %d", name, id)
	}

	// Configuration changes may take up to two minutes to become readable.
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		group, err := text.GetGroup(readyCtx, id)
		if err == nil && group.Name == name && verifyAgents(group, requiredIDs) == nil {
			return id, nil
		}

		select {
		case <-readyCtx.Done():
			return 0, fmt.Errorf("Text group %q (%d) was created but is not ready: %w", name, id, readyCtx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func verifyAgents(group textapi.Group, requiredIDs []string) error {
	if len(group.AgentPriorities) == 0 {
		return fmt.Errorf("Text group %q has no agents; add human agents before starting", group.Name)
	}

	if len(requiredIDs) != 0 {
		for _, id := range requiredIDs {
			priority, ok := group.AgentPriorities[id]
			if !ok || priority == "supervisor" {
				return fmt.Errorf("Text group %q needs routing agent %q (not supervisor)", group.Name, id)
			}
		}

		return nil
	}

	for _, priority := range group.AgentPriorities {
		if priority != "supervisor" {
			return nil
		}
	}

	return fmt.Errorf("Text group %q has no agents eligible for automatic routing", group.Name)
}
