package testenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func New() (*envtest.Environment, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	binaries, err := binaryDir(root)
	if err != nil {
		return nil, err
	}
	return &envtest.Environment{
		BinaryAssetsDirectory: binaries,
		CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}, nil
}

func moduleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("get testenv source path")
	}
	return filepath.Join(filepath.Dir(file), "..", ".."), nil
}

func binaryDir(root string) (string, error) {
	if assets := os.Getenv("KUBEBUILDER_ASSETS"); assets != "" {
		return assets, nil
	}
	dir := filepath.Join(root, "bin", "k8s")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read envtest binaries in %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("no envtest binaries in %s", dir)
}
