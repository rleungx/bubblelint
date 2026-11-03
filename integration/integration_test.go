package integration

import (
	"encoding/json"
	"flag"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binary = flag.String("golangci-lint", "", "custom golangci-lint binary to test")

func TestGolangciLint(t *testing.T) {
	if *binary == "" {
		t.Skip("run make integration to test the custom binary")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		pkg   string
		codes []string
	}{
		{"misuse", "misuse", []string{"ST001", "ST002", "ST003"}},
		{"migration", "migration", []string{"ST100", "ST101"}},
		{"clean", "clean", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(*binary, "run", "--config", ".golangci.yml", "--show-stats=false", "--output.json.path", "stdout", "--output.text.path", "", "./testdata/src/"+tc.pkg)
			cmd.Dir = root
			output, err := cmd.Output()
			if len(tc.codes) == 0 && err != nil {
				t.Fatalf("clean fixture: %v\n%s", err, output)
			}
			if len(tc.codes) > 0 {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("expected lint exit code 1: %v\n%s", err, output)
				}
			}
			var result struct {
				Issues []struct {
					FromLinter string
					Text       string
				}
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, output)
			}
			seen := make(map[string]bool)
			for _, issue := range result.Issues {
				if issue.FromLinter != "bubblelint" {
					t.Fatalf("unexpected linter: %s", issue.FromLinter)
				}
				code, _, _ := strings.Cut(issue.Text, ":")
				seen[code] = true
			}
			if len(seen) != len(tc.codes) {
				t.Fatalf("unexpected rules: %v (want %v)", seen, tc.codes)
			}
			for _, code := range tc.codes {
				if !seen[code] {
					t.Errorf("missing %s", code)
				}
			}
		})
	}
}
