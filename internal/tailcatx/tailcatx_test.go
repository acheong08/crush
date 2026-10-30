package tailcatx

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"tailscale.com/types/key"
)

func TestParseHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		addr  string
		port  uint16
		fails bool
	}{
		{in: "", addr: "", port: DefaultPort},
		{in: ":9000", addr: "", port: 9000},
		{in: "tcpABCdef", addr: "tcpABCdef", port: DefaultPort},
		{in: "tcpABCdef:9001", addr: "tcpABCdef", port: 9001},
		{in: ":0", fails: true},
		{in: "tcpABCdef:", addr: "tcpABCdef", port: DefaultPort},
	}

	for _, tc := range tests {
		addr, port, err := ParseHost(tc.in)
		if tc.fails {
			require.Error(t, err, tc.in)
			continue
		}
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.addr, addr, tc.in)
		require.Equal(t, tc.port, port, tc.in)
	}
}

func TestParsePeers(t *testing.T) {
	t.Parallel()

	peers, err := ParsePeers([]string{
		"nodekey:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, nodekey:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"",
	})
	require.NoError(t, err)
	require.Len(t, peers, 2)

	_, err = ParsePeers([]string{"not-a-key"})
	require.Error(t, err)
}

func TestClientKeyPersists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, err := ClientNodeKey(dir)
	require.NoError(t, err)
	second, err := ClientNodeKey(dir)
	require.NoError(t, err)
	require.Equal(t, first, second, "identity must survive process restarts")
	require.NotEmpty(t, first.String())
}

// TestTunnelRoundTrip exercises a real tunnel over the public DERP
// relays, so it is skipped unless explicitly enabled:
//
//	CRUSH_TAILCAT_E2E=1 go test ./internal/tailcatx/
func TestTunnelRoundTrip(t *testing.T) {
	if os.Getenv("CRUSH_TAILCAT_E2E") == "" {
		t.Skip("set CRUSH_TAILCAT_E2E=1 to run a live tailcat test")
	}

	clientDir := t.TempDir()
	serverDir := t.TempDir()

	clientKey, err := ClientNodeKey(clientDir)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	tunnel, err := Listen(ctx, serverDir, 18091, []key.NodePublic{clientKey})
	require.NoError(t, err)
	defer tunnel.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(tunnel.Listener()) }()
	defer srv.Close()

	dialer, err := NewDialer(clientDir, tunnel.URL()[len("tailcat://"):])
	require.NoError(t, err)
	defer dialer.Close()

	transport := &http.Transport{DialContext: dialer.DialContext}
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second}

	resp, err := client.Get("http://crush.invalid:18091/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
}
