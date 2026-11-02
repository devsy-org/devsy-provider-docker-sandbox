package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/distribution"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: package-provider VERSION DIST-DIR QUALIFIED-PROFILE")
		os.Exit(2)
	}
	meta, err := distribution.Generate(os.Args[1], os.Args[2], os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"provider.yaml", meta.Manifest},
		{"checksums.txt", meta.Checksums},
		{"compatibility-profile.json", meta.Profile},
	} {
		if err := os.WriteFile(filepath.Join(os.Args[2], file.name), file.data, 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
