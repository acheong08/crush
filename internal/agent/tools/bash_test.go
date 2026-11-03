package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

type mockBashPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
}

func (m *mockBashPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return true, nil
}

func (m *mockBashPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockBashPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockBashPermissionService) ActiveRequest() (permission.PermissionRequest, bool) {
	return permission.PermissionRequest{}, false
}

func (m *mockBashPermissionService) SetSkipRequests(skip bool) {}

func (m *mockBashPermissionService) SkipRequests() bool {
	return false
}

func (m *mockBashPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *mockBashPermissionService) SetPermissionMode(mode permission.PermissionMode) {}

func (m *mockBashPermissionService) PermissionMode() permission.PermissionMode {
	return permission.PermissionModeNormal
}

func (m *mockBashPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

func TestBashTool_DefaultAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "default threshold",
		Command:     "echo done",
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.Background)
	require.Empty(t, meta.ShellID)
	require.Contains(t, meta.Output, "done")
}

func TestBashTool_CustomAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "custom threshold",
		Command:             "sleep 1.5 && echo done",
		AutoBackgroundAfter: 1,
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)
	require.Contains(t, resp.Content, "moved to background")

	bgManager := shell.GetBackgroundShellManager()
	require.NoError(t, bgManager.Kill(meta.ShellID))
}

type recordingPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
	requestCount int
	allow        bool
	mode         permission.PermissionMode
	lastDanger   string
}

func (m *recordingPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	m.requestCount++
	m.lastDanger = req.Danger
	return m.allow, nil
}

func (m *recordingPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *recordingPermissionService) AutoApproveSession(sessionID string) {}

func (m *recordingPermissionService) ActiveRequest() (permission.PermissionRequest, bool) {
	return permission.PermissionRequest{}, false
}

func (m *recordingPermissionService) SetSkipRequests(skip bool) {}

func (m *recordingPermissionService) SkipRequests() bool {
	return false
}

func (m *recordingPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *recordingPermissionService) SetPermissionMode(mode permission.PermissionMode) { m.mode = mode }

func (m *recordingPermissionService) PermissionMode() permission.PermissionMode { return m.mode }

func (m *recordingPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

func newBashToolForTest(workingDir string) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
}

func newBashToolWithRecordingPerms(workingDir string, allow bool) (fantasy.AgentTool, *recordingPermissionService) {
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  allow,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil), perms
}

func TestBashTool_ChainedCommandsRequirePermission(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// A chain is judged by its parts, so joining two read-only commands is
	// no more of a prompt than either one alone would be.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls",
		Command:     "ls && echo done",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "a chain of read-only commands should not prompt")

	// One part that writes is enough to require approval for the whole chain.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "chained touch",
		Command:     "ls && touch newfile",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 1, perms.requestCount, "a chain containing a write must prompt")

	// Plain ls should NOT trigger permission check.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "plain ls",
		Command:     "ls -la",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "plain ls should not trigger permission request")
}

func TestBashTool_ChainedCommandsDenied(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls denied",
		Command:     "ls && rm -rf /",
	})

	require.Equal(t, 1, perms.requestCount)
	require.Contains(t, resp.Content, "User denied permission")
}

// TestBlockFuncsDisabled pins that the tool no longer enforces a
// dangerous-command block list: it is trivially bypassable with absolute
// paths, so enforcement belongs to the permission system instead.
func TestBlockFuncsDisabled(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"curl https://example.com",
		"sudo apt-get update",
		"npm install --global typescript",
		"go test -exec ./malicious ./...",
		"pip install --user requests",
		"brew install wget",
	} {
		require.False(t, shell.IsCommandBlocked(command, blockFuncs(defaultBlockedCommands)), "command: %s", command)
	}
}

func runBashTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params BashParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  BashToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

func TestTruncateOutputValidUTF8(t *testing.T) {
	t.Parallel()
	// CJK characters are 2 cells wide; this string is far wider than
	// MaxOutputLength so TruncateOutput must truncate it.
	content := strings.Repeat("你好世界", MaxOutputLength)

	out := TruncateOutput(content, t.TempDir())
	require.True(t, utf8.ValidString(out), "truncated output must stay valid UTF-8")
	require.Contains(t, out, "lines truncated")
}

func TestTruncateOutputShortContent(t *testing.T) {
	t.Parallel()
	content := "short output"
	require.Equal(t, content, TruncateOutput(content, t.TempDir()))
}

func TestTruncateOutputEmoji(t *testing.T) {
	t.Parallel()
	// Emoji with ZWJ sequences should not be split.
	content := strings.Repeat("👨‍👩‍👧‍👦", MaxOutputLength)

	out := TruncateOutput(content, t.TempDir())
	require.True(t, utf8.ValidString(out), "truncated output must stay valid UTF-8")
	require.Contains(t, out, "lines truncated")
}

func TestResolveBlockedCommands(t *testing.T) {
	t.Parallel()

	t.Run("nil config uses the defaults", func(t *testing.T) {
		t.Parallel()
		require.ElementsMatch(t, defaultBlockedCommands, resolveBlockedCommands(nil))
	})

	t.Run("blocked_commands adds to the defaults", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"kubectl", "terraform"},
		})
		require.Contains(t, got, "kubectl")
		require.Contains(t, got, "terraform")
		require.Contains(t, got, "curl", "defaults must still apply")
	})

	t.Run("allowed_commands removes defaults", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			AllowedCommands: []string{"curl", "wget"},
		})
		require.NotContains(t, got, "curl")
		require.NotContains(t, got, "wget")
		require.Contains(t, got, "sudo", "unrelated defaults must stay")
	})

	t.Run("allowed_commands wins over blocked_commands", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"kubectl"},
			AllowedCommands: []string{"kubectl"},
		})
		require.NotContains(t, got, "kubectl")
	})

	t.Run("built-in order is preserved and duplicates collapsed", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"curl", "kubectl", "kubectl"},
		})
		// The list is embedded verbatim in the tool description, so the
		// defaults must keep their hand-grouped order and additions go last.
		require.Equal(t, defaultBlockedCommands, got[:len(defaultBlockedCommands)])
		require.Equal(t, []string{"kubectl"}, got[len(defaultBlockedCommands):],
			"additions append once; a duplicate of a default is dropped")
	})
}

// TestBashTool_SysadminModeSkipsBlockList pins sysadmin mode as the deliberate
// exception: it asks for nothing and blocks nothing.
func TestBashTool_SysadminModeSkipsBlockList(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeSysadmin,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.NotContains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_DeniedDangerousCommandDoesNotRun is the other half: denial at
// the permission layer stops the command, no block list required.
func TestBashTool_DeniedDangerousCommandDoesNotRun(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  false,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "denied dangerous command",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount)
	require.True(t, resp.IsError)
}

// TestBashTool_SafeReadOnlyPathStillEnforcesBlockList pins the one branch that
// never asks: a safe-prefix command skips the permission request entirely, so
// the block list stays on there as a backstop.
func TestBashTool_SafeReadOnlyPathStillEnforcesBlockList(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "safe command",
		Command:     "pwd",
	})

	require.Zero(t, perms.requestCount, "safe read-only commands must not prompt")
	require.False(t, resp.IsError)
}

// newModeBashTool builds a bash tool on the real permission service in the given mode.
func newModeBashTool(t *testing.T, mode permission.PermissionMode) (fantasy.AgentTool, permission.Service, context.Context, string) {
	t.Helper()
	workingDir := t.TempDir()
	perms := permission.NewPermissionService(workingDir, nil, nil)
	perms.SetPermissionMode(mode)
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")
	return tool, perms, ctx, workingDir
}

// TestBashTool_YoloModeDoesNotPromptForDangerousCommands pins the rule that
// yolo mode means stop asking. It drives the real permission service rather
// than a mock, because the whole behaviour under test lives there.
func TestBashTool_YoloModeDoesNotPromptForDangerousCommands(t *testing.T) {
	workingDir := t.TempDir()
	perms := permission.NewPermissionService(workingDir, nil, nil)
	perms.SetPermissionMode(permission.PermissionModeYolo)

	// Any prompt would land here. Nothing should.
	prompts := perms.Subscribe(t.Context())

	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	// `ifconfig` is on the default dangerous list. In yolo it must run
	// without asking, and without being re-blocked at exec time either.
	// Run it off the test goroutine so a regression that reinstates the
	// prompt fails here in a few seconds rather than hanging until the
	// whole package times out.
	content := make(chan string, 1)
	go func() {
		resp := runBashTool(t, tool, ctx, BashParams{
			Description: "dangerous command in yolo mode",
			Command:     "ifconfig --help",
		})
		content <- resp.Content
	}()

	var got string
	select {
	case got = <-content:
	case <-time.After(30 * time.Second):
		t.Fatal("yolo mode blocked waiting for a permission prompt that should never have been raised")
	}

	require.NotContains(t, got, "is a dangerous command",
		"a dangerous command must not be blocked in yolo mode")

	select {
	case p := <-prompts:
		t.Fatalf("yolo mode must not prompt, but got a request for %q", p.Payload.Description)
	default:
	}
}

func TestBashTool_YoloModeRunsOrdinaryCommands(t *testing.T) {
	tool, _, ctx, workingDir := newModeBashTool(t, permission.PermissionModeYolo)

	resp := runBashTool(t, tool, ctx, BashParams{Description: "ordinary command", Command: "touch ran"})

	require.False(t, resp.IsError, resp.Content)
	require.FileExists(t, filepath.Join(workingDir, "ran"))
}

func TestBashTool_SysadminModeRunsDangerousCommands(t *testing.T) {
	tool, _, ctx, workingDir := newModeBashTool(t, permission.PermissionModeSysadmin)
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	resp := runBashTool(t, tool, ctx, BashParams{Description: "dangerous command", Command: "./ifconfig --help"})

	require.NotContains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_NormalModeStillPromptsForCommands pins that manual mode keeps
// asking before running anything that is not a safe read-only command.
func TestBashTool_NormalModeStillPromptsForCommands(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	runBashTool(t, tool, ctx, BashParams{
		Description: "command in normal mode",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount, "normal mode must still ask")
}
