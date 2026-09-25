package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"text-salesforce-route-chat/internal/textapi"
)

type fakeText struct {
	properties    []textapi.PropertyDefinition
	groups        []textapi.Group
	createdProps  int
	createdGroups int
	groupError    error
}

func (f *fakeText) ListPropertyDefinitions(context.Context) ([]textapi.PropertyDefinition, error) {
	return f.properties, nil
}

func (f *fakeText) CreatePropertyDefinition(_ context.Context, name string) (textapi.PropertyDefinition, error) {
	f.createdProps++
	property := textapi.PropertyDefinition{ID: "property-1", Name: name, Type: "string"}
	f.properties = append(f.properties, property)
	return property, nil
}

func (f *fakeText) ListGroups(context.Context) ([]textapi.Group, error) {
	return f.groups, nil
}

func (f *fakeText) GetGroup(_ context.Context, id int) (textapi.Group, error) {
	for _, group := range f.groups {
		if group.ID == id {
			return group, nil
		}
	}

	return textapi.Group{}, errors.New("not found")
}

func (f *fakeText) CreateGroup(_ context.Context, name string, agents map[string]string) (int, error) {
	f.createdGroups++
	if f.groupError != nil {
		return 0, f.groupError
	}

	id := 10 + f.createdGroups
	f.groups = append(f.groups, textapi.Group{ID: id, Name: name, AgentPriorities: agents})
	return id, nil
}

type fakeSalesforce struct {
	checks int
	err    error
}

func (f *fakeSalesforce) Verify(context.Context) error {
	f.checks++
	return f.err
}

func options() Options {
	return Options{
		RegularGroupName: "Regular", EnterpriseGroupName: "Enterprise",
		RegularAgentIDs:    []string{"regular@example.com"},
		EnterpriseAgentIDs: []string{"enterprise@example.com"},
	}
}

func TestBootstrapCreatesMissingResourcesOnce(t *testing.T) {
	text := &fakeText{groups: []textapi.Group{{ID: 0, Name: "General", AgentPriorities: map[string]string{"human@example.com": "normal"}}}}
	sf := &fakeSalesforce{}
	for run := range 2 {
		state, err := Run(context.Background(), text, sf, options())
		if err != nil {
			t.Fatalf("bootstrap run %d: %v", run, err)
		}

		if state.PropertyID != "property-1" || state.RegularGroupID != 11 || state.EnterpriseGroupID != 12 {
			t.Fatalf("unexpected state on run %d: %+v", run, state)
		}
	}

	if text.createdProps != 1 || text.createdGroups != 2 || sf.checks != 2 {
		t.Fatalf("bootstrap duplicated resources: properties=%d groups=%d checks=%d", text.createdProps, text.createdGroups, sf.checks)
	}
	if len(text.groups) != 3 || text.groups[0].ID != 0 || text.groups[0].Name != "General" || text.groups[1].Name != "Regular" || text.groups[1].AgentPriorities["regular@example.com"] != "normal" || text.groups[2].Name != "Enterprise" || text.groups[2].AgentPriorities["enterprise@example.com"] != "normal" {
		t.Fatalf("expected two separate staffed groups without touching default: %+v", text.groups)
	}
}

func TestBootstrapRequiresRegularAgentIDsForCreation(t *testing.T) {
	text := &fakeText{groups: []textapi.Group{
		{ID: 0, Name: "General", AgentPriorities: map[string]string{"human@example.com": "normal"}},
		{ID: 19, Name: "Enterprise", AgentPriorities: map[string]string{"enterprise@example.com": "normal"}},
	}}
	withoutIDs := options()
	withoutIDs.RegularAgentIDs = nil
	_, err := Run(context.Background(), text, &fakeSalesforce{}, withoutIDs)
	if err == nil || !strings.Contains(err.Error(), "TEXT_REGULAR_AGENT_IDS") || text.createdGroups != 0 {
		t.Fatalf("expected missing Regular agent IDs error without group creation, got %v", err)
	}
}

func TestBootstrapRejectsDefaultGroupAsRoutingDestination(t *testing.T) {
	for _, name := range []string{"Regular", "Enterprise"} {
		t.Run(name, func(t *testing.T) {
			text := &fakeText{groups: []textapi.Group{{ID: 0, Name: name, AgentPriorities: map[string]string{"human@example.com": "normal"}}}}
			_, err := Run(context.Background(), text, &fakeSalesforce{}, options())
			if err == nil || !strings.Contains(err.Error(), "must not be the default group") || text.createdGroups != 0 {
				t.Fatalf("expected default group to be rejected without creating resources, got %v", err)
			}
		})
	}
}

func TestBootstrapRequiresBothAgentListsBeforeCreatingGroups(t *testing.T) {
	text := &fakeText{groups: []textapi.Group{{ID: 0, Name: "General"}}}
	withoutEnterpriseIDs := options()
	withoutEnterpriseIDs.EnterpriseAgentIDs = nil
	_, err := Run(context.Background(), text, &fakeSalesforce{}, withoutEnterpriseIDs)
	if err == nil || !strings.Contains(err.Error(), "TEXT_ENTERPRISE_AGENT_IDS") || text.createdGroups != 0 {
		t.Fatalf("expected missing Enterprise IDs to fail before creating Regular, got %v", err)
	}
}

func TestBootstrapRejectsIncompatiblePropertyBeforeCreatingGroups(t *testing.T) {
	for _, test := range []struct {
		name     string
		property textapi.PropertyDefinition
		want     string
	}{
		{name: "wrong type", property: textapi.PropertyDefinition{ID: "id", Name: propertyName, Type: "text"}, want: "type string"},
		{name: "restricted values", property: func() textapi.PropertyDefinition {
			property := textapi.PropertyDefinition{ID: "id", Name: propertyName, Type: "string"}
			property.TypeConfiguration.AllowedValues = []string{"Basic", "Standard", "Enterprise"}
			return property
		}(), want: "must not restrict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			text := &fakeText{properties: []textapi.PropertyDefinition{test.property}}
			_, err := Run(context.Background(), text, &fakeSalesforce{}, options())
			if err == nil || !strings.Contains(err.Error(), test.want) || text.createdGroups != 0 {
				t.Fatalf("expected incompatible property error %q without group creation, got %v", test.want, err)
			}
		})
	}
}

func TestBootstrapRejectsGroupWithoutHumanRoutingAgent(t *testing.T) {
	text := &fakeText{
		properties: []textapi.PropertyDefinition{{ID: "id", Name: propertyName, Type: "string"}},
		groups: []textapi.Group{
			{ID: 11, Name: "Regular", AgentPriorities: map[string]string{"supervisor@example.com": "supervisor"}},
			{ID: 19, Name: "Enterprise", AgentPriorities: map[string]string{"enterprise@example.com": "normal"}},
		},
	}
	withoutIDs := options()
	withoutIDs.RegularAgentIDs = nil
	_, err := Run(context.Background(), text, &fakeSalesforce{}, withoutIDs)
	if err == nil || !strings.Contains(err.Error(), "no agents eligible") {
		t.Fatalf("expected missing human routing agent error, got %v", err)
	}
}

func TestBootstrapReusesBothRoutingGroupsWithoutConfiguredAgentIDs(t *testing.T) {
	text := &fakeText{groups: []textapi.Group{
		{ID: 0, Name: "General"},
		{ID: 11, Name: "Regular", AgentPriorities: map[string]string{"regular@example.com": "normal"}},
		{ID: 19, Name: "Enterprise", AgentPriorities: map[string]string{"enterprise@example.com": "normal"}},
	}}
	withoutIDs := options()
	withoutIDs.RegularAgentIDs = nil
	withoutIDs.EnterpriseAgentIDs = nil
	state, err := Run(context.Background(), text, &fakeSalesforce{}, withoutIDs)
	if err != nil || state.RegularGroupID != 11 || state.EnterpriseGroupID != 19 || text.createdGroups != 0 {
		t.Fatalf("expected to reuse both staffed routing groups, state=%+v err=%v", state, err)
	}
}

func TestBootstrapCreatesOnlyMissingRegularGroup(t *testing.T) {
	text := &fakeText{
		properties: []textapi.PropertyDefinition{{ID: "id", Name: propertyName, Type: "string"}},
		groups: []textapi.Group{
			{ID: 0, Name: "General"},
			{ID: 19, Name: "Enterprise", AgentPriorities: map[string]string{"enterprise@example.com": "normal"}},
		},
	}
	withoutIDs := options()
	withoutIDs.EnterpriseAgentIDs = nil
	state, err := Run(context.Background(), text, &fakeSalesforce{}, withoutIDs)
	if err != nil || state.RegularGroupID != 11 || state.EnterpriseGroupID != 19 || text.createdGroups != 1 {
		t.Fatalf("expected to create Regular and reuse Enterprise, state=%+v err=%v", state, err)
	}
}

func TestBootstrapExplainsForbiddenEnterpriseGroupCreation(t *testing.T) {
	text := &fakeText{
		groups: []textapi.Group{{ID: 11, Name: "Regular", AgentPriorities: map[string]string{"regular@example.com": "normal"}}},
		groupError: fmt.Errorf("create Text group: %w", &textapi.APIError{
			StatusCode: http.StatusForbidden, Message: `{"error":{"type":"authorization","message":"unauthorized"}}`,
		}),
	}
	_, err := Run(context.Background(), text, &fakeSalesforce{}, options())
	if err == nil || !strings.Contains(err.Error(), "groups--all:rw") || !strings.Contains(err.Error(), "administrator") || !strings.Contains(err.Error(), "create the group") {
		t.Fatalf("expected actionable permission error with manual fallback, got %v", err)
	}

	var apiErr *textapi.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected the Text 403 cause to remain available, got %v", err)
	}
}

func TestBootstrapRequiresSalesforceAccessBeforeStarting(t *testing.T) {
	text := &fakeText{groups: []textapi.Group{
		{ID: 11, Name: "Regular", AgentPriorities: map[string]string{"regular@example.com": "normal"}},
		{ID: 19, Name: "Enterprise", AgentPriorities: map[string]string{"enterprise@example.com": "normal"}},
	}}
	sf := &fakeSalesforce{err: errors.New("insufficient permissions")}
	_, err := Run(context.Background(), text, sf, options())
	if err == nil || !strings.Contains(err.Error(), "insufficient permissions") || sf.checks != 1 {
		t.Fatalf("expected Salesforce access check to fail startup, got %v", err)
	}
}
