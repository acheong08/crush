package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	crushlog "github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/server"
	"github.com/charmbracelet/crush/internal/tailcatx"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var (
	serverHost  string
	allowPeers  []string
	tailcatPort uint16
)

func init() {
	serverCmd.Flags().StringVarP(&serverHost, "host", "H", server.DefaultHost(), "Server host (HTTP, TCP, Unix socket, or tailcat)")
	serverCmd.Flags().StringSliceVar(&allowPeers, "allow-peer", nil, "tailcat node keys allowed to connect (repeatable or comma-separated; also CRUSH_TAILCAT_ALLOW)")
	serverCmd.Flags().Uint16Var(&tailcatPort, "tailcat-port", tailcatx.DefaultPort, "port to listen on inside the tailcat tunnel")
	rootCmd.AddCommand(serverCmd)
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Crush server",
	Long: `Start the Crush server.

Usage:

  # Serve over HTTP
  crush server --host http://127.0.0.1:8080

  # Serve over TCP
  crush server --host tcp://127.0.0.1:8080

  # Serve over a Unix socket (default)
  crush server --host unix:///tmp/crush.sock

  # Serve over an encrypted tailcat tunnel, restricted to your devices
  crush server --host tailcat:// --allow-peer nodekey:aaaa,nodekey:bbbb

While the server is running, browse the HTTP API docs at /v1/docs/
(e.g. http://127.0.0.1:8080/v1/docs/). The OpenAPI 3.1 spec is served
at /v1/docs/openapi.json.
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir, err := cmd.Flags().GetString("data-dir")
		if err != nil {
			return fmt.Errorf("failed to get data directory: %v", err)
		}
		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return fmt.Errorf("failed to get debug flag: %v", err)
		}

		cfg, err := config.Load(cmd.Context(), config.GlobalWorkspaceDir(), dataDir, debug)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %v", err)
		}

		hostURL, err := server.ParseHostURL(serverHost)
		if err != nil {
			return fmt.Errorf("invalid server host: %v", err)
		}

		logFile := filepath.Join(config.GlobalCacheDir(), "server-"+safeHostName(hostURL), "crush.log")

		if term.IsTerminal(os.Stderr.Fd()) {
			crushlog.Setup(logFile, debug, os.Stderr)
		} else {
			crushlog.Setup(logFile, debug)
		}

		srv := server.NewServer(cfg, hostURL.Scheme, hostURL.Host)
		srv.SetLogger(slog.Default())
		slog.Info("Starting Crush server...", "addr", serverHost)

		var ln net.Listener
		if hostURL.Scheme == "tailcat" {
			tunnel, err := startTailcatTunnel(cmd.Context(), hostURL.Host)
			if err != nil {
				return err
			}
			defer tunnel.Close()
			ln = tunnel.Listener()
		}

		errch := make(chan error, 1)
		go func() {
			if ln != nil {
				errch <- srv.Serve(ln)
				return
			}
			errch <- srv.ListenAndServe()
		}()

		if err = awaitStop(cmd.Context(), srv, errch); err != nil {
			if !errors.Is(err, server.ErrServerClosed) {
				_ = srv.Close()
				slog.Error("Server error", "error", err)
				return fmt.Errorf("server error: %v", err)
			}
		}

		if errors.Is(err, server.ErrServerClosed) {
			return nil
		}

		// The signal already cancelled cmd.Context(); detach so the
		// shutdown gets its full budget instead of none.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), 15*time.Second)
		defer cancel()

		slog.Info("Shutting down...")

		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("Failed to shutdown server", "error", err)
			return fmt.Errorf("failed to shutdown server: %v", err)
		}

		return nil
	},
}

// startTailcatTunnel brings up a tailcat listener and prints the
// address clients need. The allowlist is empty by default: anyone
// holding the address can connect, so the usual setup is one key per
// device you own.
func startTailcatTunnel(ctx context.Context, host string) (*tailcatx.Server, error) {
	peers, err := tailcatx.ParsePeers(append(allowPeers, os.Getenv("CRUSH_TAILCAT_ALLOW")))
	if err != nil {
		return nil, err
	}
	if len(peers) == 0 {
		slog.Warn("No --allow-peer given: any device with this address can connect")
	}
	_, port, err := tailcatx.ParseHost(host)
	if err != nil {
		return nil, err
	}
	tunnel, err := tailcatx.Listen(ctx, filepath.Dir(config.GlobalConfigData()), port, peers)
	if err != nil {
		return nil, err
	}
	slog.Info("Tailcat tunnel up", "url", tunnel.URL(), "node_key", tunnel.NodeKey(), "allowed_peers", len(peers))
	fmt.Fprintln(os.Stdout, tunnel.URL())
	return tunnel, nil
}

// awaitStop blocks until the root context is cancelled by a termination
// signal, returning nil, or the server stops on its own, returning its
// error. A server has no terminal to lose, so SIGHUP reloads config here
// instead of stopping it.
func awaitStop(ctx context.Context, srv *server.Server, errch <-chan error) error {
	signal.Reset(syscall.SIGHUP)
	hupch := make(chan os.Signal, 1)
	signal.Notify(hupch, syscall.SIGHUP)
	defer signal.Stop(hupch)

	for {
		select {
		case <-hupch:
			slog.Info("Received SIGHUP, reloading config...")
			reloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := srv.ReloadConfig(reloadCtx); err != nil {
				slog.Error("Failed to reload config", "error", err)
			}
			cancel()
		case <-ctx.Done():
			slog.Info("Received termination signal...")
			return nil
		case err := <-errch:
			return err
		}
	}
}
