package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultStateDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CRUSH_STATE_DIR", base)

	dir := defaultStateDir("/home/user/project")
	require.Equal(t, filepath.Join(base, "projects"), filepath.Dir(dir))
	require.Contains(t, filepath.Base(dir), "project-")

	// Same working directory maps to the same state dir.
	require.Equal(t, dir, defaultStateDir("/home/user/project"))
	// A different project gets a different state dir.
	require.NotEqual(t, dir, defaultStateDir("/home/user/other"))
}

func TestDefaultStateDir_SlugifiesUnusualNames(t *testing.T) {
	t.Setenv("CRUSH_STATE_DIR", t.TempDir())

	dir := defaultStateDir("/home/user/my project (v2)!")
	require.Regexp(t, `^my-project--v2-[0-9a-f]{8}$`, filepath.Base(dir))
}

func TestMigrateLegacyState(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".crush")
	state := filepath.Join(root, "state")
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "logs"), 0o755))

	writeFile := func(rel, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(legacy, rel), []byte(content), 0o600))
	}
	writeFile("crush.db", "database")
	writeFile("crush.db-wal", "wal")
	writeFile("logs/crush.log", "log line")
	writeFile("crush.json", `{"options":{}}`)
	writeFile(InitFlagFilename, "")
	writeFile(".gitignore", "*\n")
	// User-authored content that must survive.
	require.NoError(t, os.MkdirAll(filepath.Join(legacy, "skills", "mine"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "skills", "mine", "SKILL.md"), []byte("# skill"), 0o644))

	store := &ConfigStore{config: &Config{Options: &Options{
		DataDirectory:  legacy,
		StateDirectory: state,
	}}}

	require.NoError(t, MigrateLegacyState(store))

	for _, rel := range []string{"crush.db", "crush.db-wal", "crush.json", InitFlagFilename} {
		require.FileExists(t, filepath.Join(state, rel), "expected %s moved to state", rel)
		require.NoFileExists(t, filepath.Join(legacy, rel), "expected %s removed from legacy", rel)
	}
	require.FileExists(t, filepath.Join(state, "logs", "crush.log"))
	require.NoDirExists(t, filepath.Join(legacy, "logs"))

	// The generated .gitignore is gone, but user content and the dir remain.
	require.NoFileExists(t, filepath.Join(legacy, ".gitignore"))
	require.FileExists(t, filepath.Join(legacy, "skills", "mine", "SKILL.md"))

	// A second run is a no-op and does not error.
	require.NoError(t, MigrateLegacyState(store))
}

func TestMigrateLegacyState_RemovesEmptyDir(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, ".crush")
	state := filepath.Join(root, "state")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "crush.db"), []byte("db"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, ".gitignore"), []byte("*\n"), 0o644))

	store := &ConfigStore{config: &Config{Options: &Options{
		DataDirectory:  legacy,
		StateDirectory: state,
	}}}

	require.NoError(t, MigrateLegacyState(store))
	require.NoDirExists(t, legacy, "empty legacy .crush should be removed")
	require.FileExists(t, filepath.Join(state, "crush.db"))
}

func TestMigrateLegacyState_NoLegacyDir(t *testing.T) {
	root := t.TempDir()
	store := &ConfigStore{config: &Config{Options: &Options{
		DataDirectory:  filepath.Join(root, ".crush"),
		StateDirectory: filepath.Join(root, "state"),
	}}}
	require.NoError(t, MigrateLegacyState(store))
	require.NoDirExists(t, filepath.Join(root, "state"))
}

func TestMigrateLegacyState_SkipsWhenSameDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "crush.db"), []byte("db"), 0o600))

	store := &ConfigStore{config: &Config{Options: &Options{
		DataDirectory:  root,
		StateDirectory: root,
	}}}
	require.NoError(t, MigrateLegacyState(store))
	// Still there, untouched.
	require.FileExists(t, filepath.Join(root, "crush.db"))
}
