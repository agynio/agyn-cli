package cmd

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"connectrpc.com/connect"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A shell the CLI attaches to is the same shell the browser opens as a tab:
// both name one through the sandbox layout, so work started in a terminal is
// picked up in a tab and the other way round.
//
// Whether the shell survives the connection is the environment's answer, not
// this command's. Naming one costs nothing where it does not.

var cliShellIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// resolveShell decides which shell this attach names, and records it.
//
// The default is the most recently attached one -- "put me back where I was",
// which is what tmux's own attach does and what someone reconnecting means.
func resolveShell(
	ctx context.Context,
	clients *sandboxClients,
	sandboxID string,
	requested string,
	forceNew bool,
) (shellID string, shellCwd string, err error) {
	layout, err := clients.agents.GetSandboxLayout(ctx, connect.NewRequest(&agentsv1.GetSandboxLayoutRequest{
		SandboxId: sandboxID,
	}))
	if err != nil {
		// A layout the platform could not give us is not a reason to refuse a
		// shell. Attaching without an id yields an ordinary one.
		return "", "", nil
	}

	tabs := layout.Msg.GetLayout().GetTabs()
	version := layout.Msg.GetLayout().GetVersion()

	requested = strings.TrimSpace(requested)
	if requested != "" && !forceNew {
		for _, tab := range tabs {
			if tab.GetShellId() == requested || fmt.Sprint(tab.GetNumber()) == requested {
				return tab.GetShellId(), tab.GetCwd(), recordAttach(ctx, clients, sandboxID, version, tabs, tab.GetShellId())
			}
		}
		return "", "", fmt.Errorf("no shell %q in this sandbox; `agyn sandbox tabs` lists them", requested)
	}

	if !forceNew {
		if newest := mostRecentlyAttached(tabs); newest != nil {
			return newest.GetShellId(), newest.GetCwd(), recordAttach(ctx, clients, sandboxID, version, tabs, newest.GetShellId())
		}
	}

	// Nothing to return to, or --new: open one and record it so the browser
	// shows it as a tab.
	created := uuid.NewString()
	number := int32(1)
	for _, tab := range tabs {
		if tab.GetNumber() >= number {
			number = tab.GetNumber() + 1
		}
	}
	tabs = append(tabs, &agentsv1.SandboxTab{ShellId: created, Number: number})
	return created, "", recordAttach(ctx, clients, sandboxID, version, tabs, created)
}

// recordAttach writes the layout back with last_attached_at moved to the shell
// being entered, which is what makes "the one I was just in" mean anything.
//
// A failed write is not fatal: losing the record costs the tab list on the next
// visit, not the shell in front of you.
func recordAttach(
	ctx context.Context,
	clients *sandboxClients,
	sandboxID string,
	version int64,
	tabs []*agentsv1.SandboxTab,
	shellID string,
) error {
	now := timestamppb.Now()
	for _, tab := range tabs {
		if tab.GetShellId() == shellID {
			tab.LastAttachedAt = now
		}
	}
	_, _ = clients.agents.SetSandboxLayout(ctx, connect.NewRequest(&agentsv1.SetSandboxLayoutRequest{
		SandboxId: sandboxID,
		Version:   version,
		Tabs:      tabs,
	}))
	return nil
}

func mostRecentlyAttached(tabs []*agentsv1.SandboxTab) *agentsv1.SandboxTab {
	var newest *agentsv1.SandboxTab
	for _, tab := range tabs {
		if !cliShellIDPattern.MatchString(tab.GetShellId()) {
			continue
		}
		if newest == nil {
			newest = tab
			continue
		}
		if tab.GetLastAttachedAt().AsTime().After(newest.GetLastAttachedAt().AsTime()) {
			newest = tab
		}
	}
	return newest
}

func newSandboxTabsCmd() *cobra.Command {
	var args struct{ organizationID string }

	cmd := &cobra.Command{
		Use:   "tabs [NAME]",
		Short: "List your shells in a sandbox",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, positional []string) error {
			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			organizationID, err := clients.resolveOrganizationID(ctx, args.organizationID)
			if err != nil {
				return err
			}
			var name string
			if len(positional) == 1 {
				name = positional[0]
			}
			sandbox, err := clients.resolveSandbox(ctx, organizationID, name)
			if err != nil {
				return err
			}
			layout, err := clients.agents.GetSandboxLayout(ctx, connect.NewRequest(&agentsv1.GetSandboxLayoutRequest{
				SandboxId: sandbox.GetMeta().GetId(),
			}))
			if err != nil {
				return err
			}
			tabs := layout.Msg.GetLayout().GetTabs()
			sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].GetNumber() < tabs[j].GetNumber() })

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "NUMBER\tSHELL\tDIRECTORY\tLAST_ATTACHED")
			for _, tab := range tabs {
				last := "-"
				if tab.GetLastAttachedAt() != nil {
					last = tab.GetLastAttachedAt().AsTime().Local().Format("2006-01-02 15:04")
				}
				cwd := tab.GetCwd()
				if cwd == "" {
					cwd = "-"
				}
				fmt.Fprintf(out, "%d\t%s\t%s\t%s\n", tab.GetNumber(), tab.GetShellId(), cwd, last)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&args.organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}
