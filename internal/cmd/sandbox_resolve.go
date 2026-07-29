package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/spf13/cobra"
)

// sandboxClients bundles the gateway clients the sandbox commands need.
type sandboxClients struct {
	agents        gatewayv1connect.AgentsGatewayClient
	terminal      gatewayv1connect.TerminalGatewayClient
	organizations gatewayv1connect.OrganizationsGatewayClient
	runContext    *RunContext
}

func sandboxGatewayClients(cmd *cobra.Command) (*sandboxClients, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return nil, err
	}
	if runContext.Clients == nil {
		return nil, fmt.Errorf("gateway client unavailable")
	}
	httpClient := runContext.Clients.HTTPClient
	baseURL := runContext.Clients.BaseURL
	opts := runContext.Clients.ConnectOpts()
	return &sandboxClients{
		agents:        gatewayv1connect.NewAgentsGatewayClient(httpClient, baseURL, opts...),
		terminal:      gatewayv1connect.NewTerminalGatewayClient(httpClient, baseURL, opts...),
		organizations: gatewayv1connect.NewOrganizationsGatewayClient(httpClient, baseURL, opts...),
		runContext:    runContext,
	}, nil
}

// resolveOrganizationID uses the shared precedence — flag, environment, the
// profile's selection, then the caller's sole accessible organization. Sandbox
// commands are meant to be typed without ceremony, so requiring an explicit id
// when there is nothing to disambiguate would be noise.
func (c *sandboxClients) resolveOrganizationID(ctx context.Context, explicit string) (string, error) {
	return resolveOrganizationID(ctx, c.runContext, c.organizations, explicit)
}

// resolveEnvironmentID maps --env onto an environment id. Without the flag it
// defaults to the organization's sole environment, which is the common
// single-environment case.
//
// The documented `--agent @handle` convenience is not implementable yet: the
// Agent resource still carries an inline image and has no environment_id, so
// there is no environment to resolve from an agent. It arrives with the
// Flavors and Environments change.
func (c *sandboxClients) resolveEnvironmentID(ctx context.Context, organizationID, envName string) (string, error) {
	environments, err := c.listEnvironments(ctx, organizationID)
	if err != nil {
		return "", err
	}
	if name := strings.TrimSpace(envName); name != "" {
		for _, environment := range environments {
			if environment.GetName() == name {
				return environment.GetMeta().GetId(), nil
			}
		}
		return "", fmt.Errorf("environment %q not found in organization %s", name, organizationID)
	}

	switch len(environments) {
	case 0:
		return "", fmt.Errorf("no environments in organization %s; create one first", organizationID)
	case 1:
		return environments[0].GetMeta().GetId(), nil
	default:
		names := make([]string, 0, len(environments))
		for _, environment := range environments {
			names = append(names, environment.GetName())
		}
		sort.Strings(names)
		return "", fmt.Errorf("multiple environments available; pass --env <name>: %s", strings.Join(names, ", "))
	}
}

func (c *sandboxClients) listEnvironments(ctx context.Context, organizationID string) ([]*agentsv1.Environment, error) {
	var (
		environments []*agentsv1.Environment
		pageToken    string
	)
	for {
		response, err := c.agents.ListEnvironments(ctx, connect.NewRequest(&agentsv1.ListEnvironmentsRequest{
			OrganizationId: organizationID,
			PageSize:       defaultSandboxPageSize,
			PageToken:      pageToken,
		}))
		if err != nil {
			return nil, err
		}
		environments = append(environments, response.Msg.GetEnvironments()...)
		pageToken = response.Msg.GetNextPageToken()
		if pageToken == "" {
			return environments, nil
		}
	}
}

// resolveSandbox finds a sandbox by name, or — with no name — the caller's sole
// non-terminated sandbox. Ambiguity is reported as a list rather than a guess.
func (c *sandboxClients) resolveSandbox(ctx context.Context, organizationID, name string) (*agentsv1.Sandbox, error) {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		response, err := c.agents.GetSandbox(ctx, connect.NewRequest(&agentsv1.GetSandboxRequest{
			Ref: &agentsv1.GetSandboxRequest_Name{
				Name: &agentsv1.SandboxNameRef{
					OrganizationId: organizationID,
					Name:           trimmed,
				},
			},
		}))
		if err != nil {
			return nil, err
		}
		return response.Msg.GetSandbox(), nil
	}

	sandboxes, err := c.listSandboxes(ctx, organizationID, nil, false)
	if err != nil {
		return nil, err
	}
	candidates := make([]*agentsv1.Sandbox, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		if sandbox.GetStatus() != agentsv1.SandboxStatus_SANDBOX_STATUS_TERMINATED {
			candidates = append(candidates, sandbox)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("no sandboxes; start one with `agyn sandbox start`")
	case 1:
		return candidates[0], nil
	default:
		names := make([]string, 0, len(candidates))
		for _, sandbox := range candidates {
			names = append(names, fmt.Sprintf("  %-24s %s", sandbox.GetName(), sandboxStatusLabel(sandbox.GetStatus())))
		}
		sort.Strings(names)
		return nil, fmt.Errorf("multiple sandboxes; pass a name:\n%s", strings.Join(names, "\n"))
	}
}

func (c *sandboxClients) listSandboxes(ctx context.Context, organizationID string, ownerID *string, includeTerminated bool) ([]*agentsv1.Sandbox, error) {
	var (
		sandboxes []*agentsv1.Sandbox
		pageToken string
	)
	for {
		request := &agentsv1.ListSandboxesRequest{
			OrganizationId:    organizationID,
			PageSize:          defaultSandboxPageSize,
			PageToken:         pageToken,
			IncludeTerminated: includeTerminated,
		}
		if ownerID != nil {
			request.OwnerId = ownerID
		}
		response, err := c.agents.ListSandboxes(ctx, connect.NewRequest(request))
		if err != nil {
			return nil, err
		}
		sandboxes = append(sandboxes, response.Msg.GetSandboxes()...)
		pageToken = response.Msg.GetNextPageToken()
		if pageToken == "" {
			return sandboxes, nil
		}
	}
}
