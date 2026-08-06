package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"connectrpc.com/connect"
	syncv1 "github.com/agynio/agyn-cli/gen/agyn/sync/v1"
	agentsv1 "github.com/agynio/agyn-cli/gen/agynio/api/agents/v1"
	gatewayv1 "github.com/agynio/agyn-cli/gen/agynio/api/gateway/v1"
	terminalproxyv1 "github.com/agynio/agyn-cli/gen/agynio/api/terminal_proxy/v1"
	"github.com/agynio/agyn-cli/internal/sync/client"
	"github.com/agynio/agyn-cli/internal/sync/transfer"
	"github.com/agynio/agyn-cli/internal/sync/transport"
	"github.com/spf13/cobra"
)

// defaultRemoteRoot is the sandbox's workspace volume, and the root a bare
// `NAME:` addresses.
const defaultRemoteRoot = "/workspace"

func newSandboxCpCmd() *cobra.Command {
	var recursive bool
	var organizationID string

	cmd := &cobra.Command{
		Use:   "cp [-r] SRC DST",
		Short: "Copy files between the local machine and a sandbox",
		Long: "Exactly one of SRC and DST carries a NAME:path prefix naming the sandbox\n" +
			"side, the docker cp and kubectl cp convention.\n\n" +
			"A copy is one-shot: it scans, transfers what differs, applies through the\n" +
			"same staged atomic write sync uses, and exits. No daemon, no watching, no\n" +
			"reconciliation base — a copy establishes no ongoing relationship.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, destination := args[0], args[1]
			sourceRemote, sourceIsRemote := splitRemote(source)
			destRemote, destIsRemote := splitRemote(destination)

			if sourceIsRemote == destIsRemote {
				return fmt.Errorf("exactly one of SRC and DST must name a sandbox as NAME:path")
			}

			clients, err := sandboxGatewayClients(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			orgID, err := clients.resolveOrganizationID(ctx, organizationID)
			if err != nil {
				return err
			}

			spec := sourceRemote
			if destIsRemote {
				spec = destRemote
			}
			sandbox, err := clients.resolveSandbox(ctx, orgID, spec.name)
			if err != nil {
				return err
			}
			running, err := waitForRunningSandbox(ctx, clients, sandbox)
			if err != nil {
				return err
			}

			root, rel := splitRoot(spec.path)
			conn, endpoint, err := dialSync(ctx, clients, running, root)
			if err != nil {
				return err
			}
			defer conn.Close()

			if _, err := endpoint.Handshake(syncv1.MarkerMode_MARKER_MODE_READ, ""); err != nil {
				return withDiagnostics(err, conn)
			}

			var report *transfer.Report
			if destIsRemote {
				target := rel
				// A trailing slash means "into this directory", as cp does.
				if target == "" || strings.HasSuffix(spec.path, "/") {
					target = path.Join(rel, path.Base(strings.TrimSuffix(source, "/")))
				}
				report, err = transfer.Push(endpoint, source, target, recursive)
			} else {
				report, err = transfer.Pull(endpoint, rel, destination, recursive)
			}
			if err != nil {
				return withDiagnostics(err, conn)
			}

			fmt.Fprintf(os.Stderr, "copied %d file(s), %d director(ies), %d bytes\n",
				report.Files, report.Directories, report.Bytes)
			for _, skipped := range report.Skipped {
				fmt.Fprintf(os.Stderr, "skipped %s\n", skipped)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Copy directories")
	cmd.Flags().StringVar(&organizationID, "organization-id", "", "Organization ID (defaults to the selected organization)")
	return cmd
}

type remoteSpec struct {
	name string
	path string
}

// splitRemote recognizes NAME:path. A Windows-style drive letter is not a
// concern here: the CLI ships darwin and linux only.
func splitRemote(value string) (remoteSpec, bool) {
	index := strings.Index(value, ":")
	if index <= 0 {
		return remoteSpec{}, false
	}
	name := value[:index]
	if strings.ContainsAny(name, "/\\.") {
		return remoteSpec{}, false
	}
	remote := value[index+1:]
	if remote == "" {
		remote = defaultRemoteRoot
	}
	return remoteSpec{name: name, path: remote}, true
}

// splitRoot separates the directory the endpoint serves from the path within
// it. The endpoint is confined to a root, so a copy addresses the workspace and
// names its target relative to it.
func splitRoot(remotePath string) (root, rel string) {
	cleaned := path.Clean(remotePath)
	if !strings.HasPrefix(cleaned, defaultRemoteRoot) {
		// Serving the parent keeps the endpoint's confinement check meaningful
		// for paths outside the workspace, which it will refuse.
		return path.Dir(cleaned), path.Base(cleaned)
	}
	rel = strings.TrimPrefix(cleaned, defaultRemoteRoot)
	return defaultRemoteRoot, strings.TrimPrefix(rel, "/")
}

// dialSync opens a SYNC session to the sandbox. The kind is what makes it
// non-TTY with its streams kept apart; a shell session cannot carry this.
func dialSync(ctx context.Context, clients *sandboxClients, sandbox *agentsv1.Sandbox, root string) (*transport.Conn, *client.Client, error) {
	session, err := clients.terminal.CreateTerminalSession(ctx, connect.NewRequest(&gatewayv1.CreateTerminalSessionRequest{
		WorkloadId:    sandbox.GetWorkloadId(),
		ContainerName: sandboxMainContainer,
		Kind:          terminalproxyv1.SessionKind_SESSION_KIND_SYNC,
		SyncRoot:      root,
	}))
	if err != nil {
		return nil, nil, err
	}
	conn, err := transport.Dial(ctx, session.Msg.GetWebsocketUrl(), session.Msg.GetTicket(), clients.runContext.Clients.HTTPClient)
	if err != nil {
		return nil, nil, err
	}
	return conn, client.New(conn, conn), nil
}

// withDiagnostics attaches whatever the endpoint wrote to stderr. Without it a
// protocol failure reads as a transport error with no cause.
func withDiagnostics(err error, conn *transport.Conn) error {
	if diagnostics := conn.Diagnostics(); diagnostics != "" {
		return fmt.Errorf("%w\nsandbox: %s", err, diagnostics)
	}
	return err
}
