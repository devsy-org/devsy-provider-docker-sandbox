package main

import (
	"fmt"
	"os"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/distribution"
)

func main() { os.Exit(run()) }

func run() int {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: package-provider VERSION DIST-DIR QUALIFIED-PROFILE")
		return 2
	}
	meta, err := distribution.Generate(os.Args[1], os.Args[2], os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	root, err := os.OpenRoot(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = root.Close() }()
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"provider.yaml", meta.Manifest},
		{"checksums.txt", meta.Checksums},
		{"compatibility-profile.json", meta.Profile},
	} {
		if err := root.WriteFile(file.name, file.data, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}
