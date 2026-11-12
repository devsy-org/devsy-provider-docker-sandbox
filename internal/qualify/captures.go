package qualify

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

var captureName = regexp.MustCompile(`^[A-Za-z0-9._-]+\.(json|txt)$`)

type capture struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

func VerifyCaptures(directory string) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile("provenance.json")
	if err != nil {
		return "", err
	}
	var provenance struct {
		Captures []capture `json:"captures"`
	}
	if err := json.Unmarshal(data, &provenance); err != nil {
		return "", fmt.Errorf("unreadable provenance: %w", err)
	}
	if err := verifyCaptureSet(root, provenance.Captures); err != nil {
		return "", err
	}
	return SHA256Hex(data), nil
}

func verifyCaptureSet(root *os.Root, captures []capture) error {
	if len(captures) < 4 {
		return fmt.Errorf(
			"provenance has %d captures, want at least 4",
			len(captures),
		)
	}
	kinds := make(map[string]bool)
	for _, c := range captures {
		if err := verifyCapture(root, c); err != nil {
			return err
		}
		kinds[c.Kind] = true
	}
	for _, kind := range []string{"failure", "stop_result", "rm_result"} {
		if !kinds[kind] {
			return fmt.Errorf("provenance lacks capture kind %s", kind)
		}
	}
	return nil
}

func verifyCapture(root *os.Root, c capture) error {
	if !captureName.MatchString(c.File) || !hex64.MatchString(c.SHA256) {
		return fmt.Errorf("capture %q has an invalid filename or sha256", c.File)
	}
	data, err := root.ReadFile(c.File)
	if err != nil {
		return fmt.Errorf("capture %s: %w", c.File, err)
	}
	if len(data) == 0 || SHA256Hex(data) != c.SHA256 {
		return fmt.Errorf("capture %s is empty or its sha256 differs from provenance", c.File)
	}
	return nil
}
