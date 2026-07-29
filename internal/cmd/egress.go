package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	egressv1 "github.com/agynio/agyn-cli/gen/agynio/api/egress/v1"
	gatewayv1connect "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1/gatewayv1connect"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

type egressRuleOutput struct {
	ID             string              `json:"id" yaml:"id"`
	OrganizationID string              `json:"organization_id" yaml:"organization_id"`
	Name           string              `json:"name" yaml:"name"`
	Description    string              `json:"description,omitempty" yaml:"description,omitempty"`
	Matcher        egressMatcherOutput `json:"matcher" yaml:"matcher"`
	Effect         egressEffectOutput  `json:"effect" yaml:"effect"`
	CreatedAt      string              `json:"created_at" yaml:"created_at"`
	UpdatedAt      string              `json:"updated_at" yaml:"updated_at"`
}

type egressMatcherOutput struct {
	DomainPattern string   `json:"domain_pattern" yaml:"domain_pattern"`
	Ports         []int32  `json:"ports,omitempty" yaml:"ports,omitempty"`
	Methods       []string `json:"methods,omitempty" yaml:"methods,omitempty"`
	PathPattern   string   `json:"path_pattern,omitempty" yaml:"path_pattern,omitempty"`
}

type egressEffectOutput struct {
	Action  string               `json:"action" yaml:"action"`
	Headers []egressHeaderOutput `json:"headers,omitempty" yaml:"headers,omitempty"`
}

type egressHeaderOutput struct {
	Name     string `json:"name" yaml:"name"`
	Scheme   string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Source   string `json:"source" yaml:"source"`
	SecretID string `json:"secret_id,omitempty" yaml:"secret_id,omitempty"`
}

type egressAttachmentOutput struct {
	ID        string `json:"id" yaml:"id"`
	RuleID    string `json:"rule_id" yaml:"rule_id"`
	AgentID   string `json:"agent_id" yaml:"agent_id"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	UpdatedAt string `json:"updated_at" yaml:"updated_at"`
}

type egressRuleArgs struct {
	organizationID string
	name           string
	description    string
	domain         string
	ports          []int32
	methods        []string
	path           string
	action         string
	headers        []string
	pageSize       int32
	pageToken      string
}

func newEgressCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "egress", Short: "Manage egress rules"}
	cmd.AddCommand(newEgressRuleCmd())
	return cmd
}

func newEgressRuleCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "rule", Short: "Manage egress rules"}
	cmd.AddCommand(newEgressRuleCreateCmd())
	cmd.AddCommand(newEgressRuleListCmd())
	cmd.AddCommand(newEgressRuleGetCmd())
	cmd.AddCommand(newEgressRuleUpdateCmd())
	cmd.AddCommand(newEgressRuleDeleteCmd())
	cmd.AddCommand(newEgressRuleAttachCmd())
	cmd.AddCommand(newEgressRuleDetachCmd())
	return cmd
}

func newEgressRuleCreateCmd() *cobra.Command {
	args := &egressRuleArgs{action: "allow"}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an egress rule",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			matcher, err := buildEgressMatcher(args)
			if err != nil {
				return err
			}
			effect, err := buildEgressEffect(args)
			if err != nil {
				return err
			}
			response, err := client.CreateEgressRule(cmd.Context(), connect.NewRequest(&egressv1.CreateEgressRuleRequest{
				OrganizationId: organizationID,
				Name:           args.name,
				Description:    args.description,
				Matcher:        matcher,
				Effect:         effect,
			}))
			if err != nil {
				return err
			}
			return printEgressRule(runContext.OutputFormat, response.Msg.GetEgressRule())
		},
	}
	bindEgressRuleInputFlags(cmd, args)
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("domain")
	return cmd
}

func newEgressRuleListCmd() *cobra.Command {
	args := &egressRuleArgs{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List egress rules",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, runContext, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			if args.pageSize < 0 {
				return fmt.Errorf("page-size must be non-negative")
			}
			organizationID, err := organizationIDForCommand(cmd, args.organizationID)
			if err != nil {
				return err
			}
			response, err := client.ListEgressRules(cmd.Context(), connect.NewRequest(&egressv1.ListEgressRulesRequest{
				OrganizationId: organizationID,
				PageSize:       args.pageSize,
				PageToken:      args.pageToken,
			}))
			if err != nil {
				return err
			}
			rules := response.Msg.GetEgressRules()
			outputs := make([]egressRuleOutput, 0, len(rules))
			rows := make([][]string, 0, len(rules))
			for _, rule := range rules {
				out, err := egressRuleOutputFrom(rule)
				if err != nil {
					return err
				}
				outputs = append(outputs, out)
				rows = append(rows, []string{out.ID, out.Name, out.Matcher.DomainPattern, strings.Join(int32SliceStrings(out.Matcher.Ports), ","), out.Effect.Action, out.UpdatedAt})
			}
			if runContext.OutputFormat == output.FormatTable {
				return output.Print(runContext.OutputFormat, output.Table{Headers: []string{"ID", "NAME", "DOMAIN", "PORTS", "ACTION", "UPDATED_AT"}, Rows: rows})
			}
			return output.Print(runContext.OutputFormat, outputs)
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().Int32Var(&args.pageSize, "page-size", 0, "Page size")
	cmd.Flags().StringVar(&args.pageToken, "page-token", "", "Page token")
	return cmd
}

func newEgressRuleGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get an egress rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.GetEgressRule(cmd.Context(), connect.NewRequest(&egressv1.GetEgressRuleRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			return printEgressRule(runContext.OutputFormat, response.Msg.GetEgressRule())
		},
	}
	return cmd
}

func newEgressRuleUpdateCmd() *cobra.Command {
	args := &egressRuleArgs{}
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an egress rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			request := &egressv1.UpdateEgressRuleRequest{Id: input[0]}
			var current *egressv1.EgressRule
			if matcherFlagsChanged(cmd) || effectFlagsChanged(cmd) {
				response, err := client.GetEgressRule(cmd.Context(), connect.NewRequest(&egressv1.GetEgressRuleRequest{Id: input[0]}))
				if err != nil {
					return err
				}
				current = response.Msg.GetEgressRule()
			}
			if cmd.Flags().Changed("name") {
				request.Name = &args.name
			}
			if cmd.Flags().Changed("description") {
				request.Description = &args.description
			}
			if matcherFlagsChanged(cmd) {
				matcher, err := buildMergedEgressMatcher(cmd, args, current.GetMatcher())
				if err != nil {
					return err
				}
				request.Matcher = matcher
			}
			if effectFlagsChanged(cmd) {
				effect, err := buildMergedEgressEffect(cmd, args, current.GetEffect())
				if err != nil {
					return err
				}
				request.Effect = effect
			}
			response, err := client.UpdateEgressRule(cmd.Context(), connect.NewRequest(request))
			if err != nil {
				return err
			}
			return printEgressRule(runContext.OutputFormat, response.Msg.GetEgressRule())
		},
	}
	bindEgressRuleInputFlags(cmd, args)
	return cmd
}

func newEgressRuleDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an egress rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeleteEgressRule(cmd.Context(), connect.NewRequest(&egressv1.DeleteEgressRuleRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted egress rule %s\n", input[0])
			return err
		},
	}
	return cmd
}

func newEgressRuleAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <rule-id> <agent-id>",
		Short: "Attach an egress rule to an agent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, runContext, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			response, err := client.CreateEgressRuleAttachment(cmd.Context(), connect.NewRequest(&egressv1.CreateEgressRuleAttachmentRequest{RuleId: input[0], AgentId: input[1]}))
			if err != nil {
				return err
			}
			return printEgressAttachment(runContext.OutputFormat, response.Msg.GetEgressRuleAttachment())
		},
	}
	return cmd
}

func newEgressRuleDetachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <attachment-id>",
		Short: "Detach an egress rule attachment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, input []string) error {
			client, _, err := egressGatewayClient(cmd)
			if err != nil {
				return err
			}
			_, err = client.DeleteEgressRuleAttachment(cmd.Context(), connect.NewRequest(&egressv1.DeleteEgressRuleAttachmentRequest{Id: input[0]}))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted egress rule attachment %s\n", input[0])
			return err
		},
	}
	return cmd
}

func bindEgressRuleInputFlags(cmd *cobra.Command, args *egressRuleArgs) {
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	cmd.Flags().StringVar(&args.name, "name", "", "Rule name")
	cmd.Flags().StringVar(&args.description, "description", "", "Rule description")
	cmd.Flags().StringVar(&args.domain, "domain", "", "Destination domain pattern")
	cmd.Flags().Int32SliceVar(&args.ports, "port", nil, "Destination port; repeat or comma-separate")
	cmd.Flags().StringSliceVar(&args.methods, "method", nil, "HTTP method; repeat or comma-separate")
	cmd.Flags().StringVar(&args.path, "path", "", "Request path glob")
	cmd.Flags().StringVar(&args.action, "action", "", "Rule action: allow or deny")
	cmd.Flags().StringSliceVar(&args.headers, "header", nil, "Injected header NAME=VALUE, NAME=secret:ID, or NAME=bearer-secret:ID")
}

func egressGatewayClient(cmd *cobra.Command) (gatewayv1connect.EgressRulesGatewayClient, *RunContext, error) {
	runContext, err := RunContextFrom(cmd)
	if err != nil {
		return nil, nil, err
	}
	if runContext.Clients == nil {
		return nil, nil, fmt.Errorf("gateway client unavailable")
	}
	client := gatewayv1connect.NewEgressRulesGatewayClient(runContext.Clients.HTTPClient, runContext.Clients.BaseURL, runContext.Clients.ConnectOpts()...)
	return client, runContext, nil
}

func matcherFlagsChanged(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("domain") || cmd.Flags().Changed("port") || cmd.Flags().Changed("method") || cmd.Flags().Changed("path")
}

func effectFlagsChanged(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("action") || cmd.Flags().Changed("header")
}

func buildMergedEgressMatcher(cmd *cobra.Command, args *egressRuleArgs, current *egressv1.EgressRuleMatcher) (*egressv1.EgressRuleMatcher, error) {
	if current == nil {
		return nil, fmt.Errorf("egress rule matcher missing from response")
	}
	merged := &egressRuleArgs{
		domain:  current.GetDomainPattern(),
		ports:   append([]int32(nil), current.GetPorts()...),
		methods: append([]string(nil), current.GetMethods()...),
		path:    current.GetPathPattern(),
	}
	if cmd.Flags().Changed("domain") {
		merged.domain = args.domain
	}
	if cmd.Flags().Changed("port") {
		merged.ports = args.ports
	}
	if cmd.Flags().Changed("method") {
		merged.methods = args.methods
	}
	if cmd.Flags().Changed("path") {
		merged.path = args.path
	}
	return buildEgressMatcher(merged)
}

func buildMergedEgressEffect(cmd *cobra.Command, args *egressRuleArgs, current *egressv1.EgressRuleEffect) (*egressv1.EgressRuleEffect, error) {
	if current == nil {
		return nil, fmt.Errorf("egress rule effect missing from response")
	}
	merged := &egressRuleArgs{headers: egressHeaderArgsFromProto(current.GetInject())}
	if current.GetAction() != egressv1.EgressRuleAction_EGRESS_RULE_ACTION_UNSPECIFIED {
		merged.action = egressActionString(current.GetAction())
	}
	if cmd.Flags().Changed("action") {
		merged.action = args.action
	}
	if cmd.Flags().Changed("header") {
		merged.headers = args.headers
	}
	return buildEgressEffect(merged)
}

func egressHeaderArgsFromProto(headers []*egressv1.EgressRuleHeader) []string {
	values := make([]string, 0, len(headers))
	for _, header := range headers {
		credential := header.GetValue()
		if header.GetSecretId() != "" {
			prefix := "secret:"
			switch header.GetScheme() {
			case egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER:
				prefix = "bearer-secret:"
			case egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BASIC:
				prefix = "basic-secret:"
			case egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_UNSPECIFIED:
			default:
				panic("unsupported egress header scheme " + header.GetScheme().String())
			}
			credential = prefix + header.GetSecretId()
		}
		values = append(values, header.GetName()+"="+credential)
	}
	return values
}

func buildEgressMatcher(args *egressRuleArgs) (*egressv1.EgressRuleMatcher, error) {
	if strings.TrimSpace(args.domain) == "" {
		return nil, fmt.Errorf("domain is required")
	}
	methods := make([]string, 0, len(args.methods))
	for _, method := range args.methods {
		trimmed := strings.ToUpper(strings.TrimSpace(method))
		if trimmed == "" {
			return nil, fmt.Errorf("method cannot be empty")
		}
		methods = append(methods, trimmed)
	}
	return &egressv1.EgressRuleMatcher{DomainPattern: strings.TrimSpace(args.domain), Ports: args.ports, Methods: methods, PathPattern: strings.TrimSpace(args.path)}, nil
}

func buildEgressEffect(args *egressRuleArgs) (*egressv1.EgressRuleEffect, error) {
	effect := &egressv1.EgressRuleEffect{}
	if strings.TrimSpace(args.action) != "" {
		action, err := parseEgressAction(args.action)
		if err != nil {
			return nil, err
		}
		effect.Action = action.Enum()
	}
	headers, err := parseEgressHeaders(args.headers)
	if err != nil {
		return nil, err
	}
	effect.Inject = headers
	return effect, nil
}

func parseEgressAction(value string) (egressv1.EgressRuleAction, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow":
		return egressv1.EgressRuleAction_EGRESS_RULE_ACTION_ALLOW, nil
	case "deny":
		return egressv1.EgressRuleAction_EGRESS_RULE_ACTION_DENY, nil
	default:
		return egressv1.EgressRuleAction_EGRESS_RULE_ACTION_UNSPECIFIED, fmt.Errorf("action must be allow or deny")
	}
}

func parseEgressHeaders(values []string) ([]*egressv1.EgressRuleHeader, error) {
	headers := make([]*egressv1.EgressRuleHeader, 0, len(values))
	for _, value := range values {
		name, credential, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header must use NAME=VALUE")
		}
		header := &egressv1.EgressRuleHeader{Name: strings.TrimSpace(name)}
		credential = strings.TrimSpace(credential)
		switch {
		case strings.HasPrefix(credential, "bearer-secret:"):
			header.Scheme = egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER
			header.Credential = &egressv1.EgressRuleHeader_SecretId{SecretId: strings.TrimPrefix(credential, "bearer-secret:")}
		case strings.HasPrefix(credential, "basic-secret:"):
			header.Scheme = egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BASIC
			header.Credential = &egressv1.EgressRuleHeader_SecretId{SecretId: strings.TrimPrefix(credential, "basic-secret:")}
		case strings.HasPrefix(credential, "secret:"):
			header.Credential = &egressv1.EgressRuleHeader_SecretId{SecretId: strings.TrimPrefix(credential, "secret:")}
		default:
			header.Credential = &egressv1.EgressRuleHeader_Value{Value: credential}
		}
		headers = append(headers, header)
	}
	return headers, nil
}

func printEgressRule(format output.Format, rule *egressv1.EgressRule) error {
	out, err := egressRuleOutputFrom(rule)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "NAME", "DOMAIN", "PORTS", "ACTION", "UPDATED_AT"}, Rows: [][]string{{out.ID, out.Name, out.Matcher.DomainPattern, strings.Join(int32SliceStrings(out.Matcher.Ports), ","), out.Effect.Action, out.UpdatedAt}}})
	}
	return output.Print(format, out)
}

func printEgressAttachment(format output.Format, attachment *egressv1.EgressRuleAttachment) error {
	out, err := egressAttachmentOutputFrom(attachment)
	if err != nil {
		return err
	}
	if format == output.FormatTable {
		return output.Print(format, output.Table{Headers: []string{"ID", "RULE_ID", "AGENT_ID", "CREATED_AT"}, Rows: [][]string{{out.ID, out.RuleID, out.AgentID, out.CreatedAt}}})
	}
	return output.Print(format, out)
}

func egressRuleOutputFrom(rule *egressv1.EgressRule) (egressRuleOutput, error) {
	if rule == nil {
		return egressRuleOutput{}, fmt.Errorf("egress rule missing from response")
	}
	meta := rule.GetMeta()
	if meta == nil {
		return egressRuleOutput{}, fmt.Errorf("egress rule meta missing from response")
	}
	matcher := rule.GetMatcher()
	effect := rule.GetEffect()
	return egressRuleOutput{ID: meta.GetId(), OrganizationID: rule.GetOrganizationId(), Name: rule.GetName(), Description: rule.GetDescription(), Matcher: egressMatcherOutput{DomainPattern: matcher.GetDomainPattern(), Ports: matcher.GetPorts(), Methods: matcher.GetMethods(), PathPattern: matcher.GetPathPattern()}, Effect: egressEffectOutputFrom(effect), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func egressEffectOutputFrom(effect *egressv1.EgressRuleEffect) egressEffectOutput {
	out := egressEffectOutput{Action: egressActionString(effect.GetAction())}
	for _, header := range effect.GetInject() {
		headerOut := egressHeaderOutput{Name: header.GetName(), Scheme: egressSchemeString(header.GetScheme()), Source: "value"}
		if header.GetSecretId() != "" {
			headerOut.Source = "secret"
			headerOut.SecretID = header.GetSecretId()
		}
		out.Headers = append(out.Headers, headerOut)
	}
	return out
}

func egressAttachmentOutputFrom(attachment *egressv1.EgressRuleAttachment) (egressAttachmentOutput, error) {
	if attachment == nil {
		return egressAttachmentOutput{}, fmt.Errorf("egress rule attachment missing from response")
	}
	meta := attachment.GetMeta()
	if meta == nil {
		return egressAttachmentOutput{}, fmt.Errorf("egress rule attachment meta missing from response")
	}
	return egressAttachmentOutput{ID: meta.GetId(), RuleID: attachment.GetRuleId(), AgentID: attachment.GetAgentId(), CreatedAt: formatTimestamp(meta.GetCreatedAt()), UpdatedAt: formatTimestamp(meta.GetUpdatedAt())}, nil
}

func egressActionString(action egressv1.EgressRuleAction) string {
	switch action {
	case egressv1.EgressRuleAction_EGRESS_RULE_ACTION_ALLOW:
		return "allow"
	case egressv1.EgressRuleAction_EGRESS_RULE_ACTION_DENY:
		return "deny"
	case egressv1.EgressRuleAction_EGRESS_RULE_ACTION_UNSPECIFIED:
		return "unspecified"
	default:
		panic("unsupported egress rule action " + action.String())
	}
}

func egressSchemeString(scheme egressv1.HeaderAuthScheme) string {
	switch scheme {
	case egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BEARER:
		return "bearer"
	case egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_BASIC:
		return "basic"
	case egressv1.HeaderAuthScheme_HEADER_AUTH_SCHEME_UNSPECIFIED:
		return ""
	default:
		panic("unsupported egress header scheme " + scheme.String())
	}
}

func int32SliceStrings(values []int32) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strconv.FormatInt(int64(value), 10))
	}
	return out
}

func init() {
	rootCmd.AddCommand(newEgressCmd())
}
