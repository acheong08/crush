package backend

import (
	"fmt"
	"os"
)

func createStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory: %q %w", dir, err)
	}
	return nil
}
