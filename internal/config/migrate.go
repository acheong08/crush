package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MigrateLegacyState moves per-project runtime state that older Crush
// versions wrote into a .crush directory next to the project into the
// centralized state directory. Only machine-owned files are moved: the
// SQLite database, logs, workspace overrides, and the init flag. Anything the
// user authored in .crush (skills, commands) is left untouched, and the
// directory is removed only when it ends up empty.
//
// Migration is best-effort and idempotent: it does nothing once the state
// directory already holds a database, and never overwrites existing state.
func MigrateLegacyState(store *ConfigStore) error {
	if store == nil {
		return nil
	}
	cfg := store.Config()
	if cfg == nil || cfg.Options == nil {
		return nil
	}

	legacy := cfg.Options.DataDirectory
	state := cfg.Options.StateDirectory
	if legacy == "" || state == "" {
		return nil
	}

	absLegacy, err := filepath.Abs(legacy)
	if err != nil {
		return nil
	}
	absState, err := filepath.Abs(state)
	if err != nil {
		return nil
	}
	if absLegacy == absState {
		return nil
	}

	// Nothing to migrate if the legacy directory was never created.
	if _, err := os.Stat(absLegacy); err != nil {
		return nil
	}
	// Already migrated once the state database exists.
	if _, err := os.Stat(filepath.Join(absState, "crush.db")); err == nil {
		return nil
	}

	if err := os.MkdirAll(absState, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %q: %w", absState, err)
	}

	for _, name := range []string{
		"crush.db",
		"crush.db-wal",
		"crush.db-shm",
		appName + ".json",
		InitFlagFilename,
	} {
		if err := moveIfAbsent(filepath.Join(absLegacy, name), filepath.Join(absState, name)); err != nil {
			return err
		}
	}

	if err := moveDirIfAbsent(filepath.Join(absLegacy, "logs"), filepath.Join(absState, "logs")); err != nil {
		return err
	}

	cleanupLegacyDir(absLegacy)
	return nil
}

// moveIfAbsent moves src to dst unless src is missing or dst already exists.
func moveIfAbsent(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	return movePath(src, dst)
}

// moveDirIfAbsent moves the directory src to dst unless src is missing, is not
// a directory, or dst already exists.
func moveDirIfAbsent(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil || !fi.IsDir() {
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	return movePath(src, dst)
}

// movePath renames src to dst, falling back to a copy and delete when the two
// locations live on different filesystems.
func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyPath(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// copyPath copies a file or directory tree from src to dst.
func copyPath(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, fi.Mode())
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := copyFile(s, d, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cleanupLegacyDir removes the generated .gitignore and then the legacy
// directory if it is empty. A directory still holding user content is kept.
func cleanupLegacyDir(dir string) {
	gitignore := filepath.Join(dir, ".gitignore")
	if data, err := os.ReadFile(gitignore); err == nil && strings.TrimSpace(string(data)) == "*" {
		_ = os.Remove(gitignore)
	}
	_ = os.Remove(dir)
}
