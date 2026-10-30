// Package tailcatx connects Crush's client/server protocol to a tailcat
// tunnel: a WireGuard data plane with no coordination server, so peers
// authenticate with node keys instead of an account system.
package tailcatx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// DefaultPort is the port a Crush server listens on inside the tunnel
// when the host does not name one.
const DefaultPort uint16 = 8080

const (
	serverStateFile = "tailcat-server.json"
	clientStateFile = "tailcat-client.json"
)

// serverState is the on-disk identity of a tailcat server. Both keys
// must survive restarts or the address printed to clients stops working.
type serverState struct {
	NodeKey      string `json:"node_key"`
	PreSharedKey string `json:"pre_shared_key"`
}

// clientState is the on-disk identity of a tailcat client. It is what
// servers allowlist, so it has to be stable across invocations.
type clientState struct {
	NodeKey string `json:"node_key"`
}

func logf(format string, args ...any) {
	slog.Debug("tailcat: " + fmt.Sprintf(format, args...))
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return true, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ParseHost splits a tailcat host into its address and port. Accepted
// forms are "<addr>", "<addr>:<port>", ":<port>", and "" (server side,
// which mints its own address).
func ParseHost(host string) (string, uint16, error) {
	if host == "" {
		return "", DefaultPort, nil
	}
	if strings.HasPrefix(host, ":") {
		port, err := parsePort(host[1:])
		if err != nil {
			return "", 0, err
		}
		return "", port, nil
	}
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		portStr := host[i+1:]
		if portStr == "" {
			return host[:i], DefaultPort, nil
		}
		if port, err := parsePort(portStr); err == nil {
			return host[:i], port, nil
		}
	}
	return host, DefaultPort, nil
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return uint16(n), nil
}

// ParsePeers parses allowlisted peer node keys. Each entry may hold a
// single key or a comma-separated list, so a device fleet can be
// configured from one environment variable.
func ParsePeers(values []string) ([]key.NodePublic, error) {
	var peers []key.NodePublic
	for _, value := range values {
		for _, raw := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n' || r == '\t'
		}) {
			var peer key.NodePublic
			if err := peer.UnmarshalText([]byte(raw)); err != nil {
				return nil, fmt.Errorf("invalid peer key %q: %w", raw, err)
			}
			peers = append(peers, peer)
		}
	}
	return peers, nil
}

// Server is a tailcat listener carrying the Crush HTTP API.
type Server struct {
	tc      *tailcat.Server
	ln      net.Listener
	port    uint16
	nodeKey key.NodePublic
}

// Listen starts (or resumes) a tailcat server listening on the given
// port, restricted to allowed peers when the list is non-empty.
func Listen(ctx context.Context, stateDir string, port uint16, allowed []key.NodePublic) (*Server, error) {
	statePath := filepath.Join(stateDir, serverStateFile)

	var (
		st      serverState
		nodeKey key.NodePrivate
		psk     tailcat.PresharedKey
	)
	exists, err := readJSON(statePath, &st)
	if err != nil {
		return nil, err
	}
	if exists {
		if err := nodeKey.UnmarshalText([]byte(st.NodeKey)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", statePath, err)
		}
		if err := psk.UnmarshalText([]byte(st.PreSharedKey)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", statePath, err)
		}
	}
	if nodeKey.IsZero() {
		nodeKey = key.NewNode()
	}
	if psk.IsZero() {
		psk = tailcat.NewPresharedKey()
	}

	nodeKeyText, err := nodeKey.MarshalText()
	if err != nil {
		return nil, err
	}
	pskText, err := psk.MarshalText()
	if err != nil {
		return nil, err
	}
	if err := writeJSON(statePath, serverState{
		NodeKey:      string(nodeKeyText),
		PreSharedKey: string(pskText),
	}); err != nil {
		return nil, fmt.Errorf("persist tailcat identity: %w", err)
	}

	tc := &tailcat.Server{
		Key:            nodeKey,
		PresharedKey:   psk,
		AllowedClients: allowed,
		Logf:           logf,
	}
	ln, err := tc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		_ = tc.Close()
		return nil, fmt.Errorf("tailcat listen: %w", err)
	}
	return &Server{tc: tc, ln: ln, port: port, nodeKey: nodeKey.Public()}, nil
}

// Listener returns the listener carrying the tunnel's TCP streams.
func (s *Server) Listener() net.Listener { return s.ln }

// NodeKey returns the server's public node key in "nodekey:..." form.
func (s *Server) NodeKey() string { return s.nodeKey.String() }

// URL returns the address clients dial.
func (s *Server) URL() string {
	url := "tailcat://" + string(s.tc.TailcatAddr())
	if s.port != DefaultPort {
		url += ":" + strconv.FormatUint(uint64(s.port), 10)
	}
	return url
}

// Close shuts down the tunnel.
func (s *Server) Close() error { return s.tc.Close() }

// Dialer dials a Crush server through a tailcat tunnel.
type Dialer struct {
	client *tailcat.Client
	port   uint16
}

// NewDialer builds a dialer for the given tailcat address, reusing this
// machine's persistent node identity so servers can allowlist it.
func NewDialer(stateDir, rawAddr string) (*Dialer, error) {
	addr, port, err := ParseHost(rawAddr)
	if err != nil {
		return nil, err
	}
	if addr == "" {
		return nil, errors.New("tailcat address is empty")
	}
	nodeKey, err := loadOrCreateClientKey(stateDir)
	if err != nil {
		return nil, err
	}
	client := tailcat.NewClient(tailcat.Addr(addr))
	client.Key = nodeKey
	client.Logf = logf
	return &Dialer{client: client, port: port}, nil
}

// DialContext implements the signature net/http expects from a
// transport dialer. The tunnel address is fixed by the tailcat address,
// so the requested network and address are ignored.
func (d *Dialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return d.client.DialTCPPort(ctx, d.port)
}

// Close releases the tunnel.
func (d *Dialer) Close() error { return d.client.Close() }

// ClientNodeKey returns this machine's stable public node key, the
// value a server needs in its allowlist.
func ClientNodeKey(stateDir string) (key.NodePublic, error) {
	nodeKey, err := loadOrCreateClientKey(stateDir)
	if err != nil {
		return key.NodePublic{}, err
	}
	return nodeKey.Public(), nil
}

func loadOrCreateClientKey(stateDir string) (key.NodePrivate, error) {
	statePath := filepath.Join(stateDir, clientStateFile)
	var st clientState
	exists, err := readJSON(statePath, &st)
	if err != nil {
		return key.NodePrivate{}, err
	}
	if exists {
		var nodeKey key.NodePrivate
		if err := nodeKey.UnmarshalText([]byte(st.NodeKey)); err != nil {
			return key.NodePrivate{}, fmt.Errorf("parse %s: %w", statePath, err)
		}
		if !nodeKey.IsZero() {
			return nodeKey, nil
		}
	}
	nodeKey := key.NewNode()
	text, err := nodeKey.MarshalText()
	if err != nil {
		return key.NodePrivate{}, err
	}
	if err := writeJSON(statePath, clientState{NodeKey: string(text)}); err != nil {
		return key.NodePrivate{}, fmt.Errorf("persist tailcat client identity: %w", err)
	}
	return nodeKey, nil
}
