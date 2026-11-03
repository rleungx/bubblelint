package bubblelint

import (
	"fmt"
	"strings"
)

// Options selects checks to run. A nil Checks slice enables all checks;
// an empty slice disables all checks. Entries are applied from left to right,
// starting with no checks: use a rule ID or "all" to enable checks, and prefix
// either with "-" to disable them.
type Options struct {
	Checks []string
}

// Validate reports invalid check names or selection syntax.
func (o Options) Validate() error {
	_, err := parseChecks(o.Checks)
	return err
}

type checkSet uint8

const (
	checkST001 checkSet = 1 << iota
	checkST002
	checkST003
	checkST100
	checkST101
	misuseChecks    = checkST001 | checkST002 | checkST003
	migrationChecks = checkST100 | checkST101
	allChecks       = misuseChecks | migrationChecks
)

func checkID(name string) checkSet {
	switch name {
	case "ST001":
		return checkST001
	case "ST002":
		return checkST002
	case "ST003":
		return checkST003
	case "ST100":
		return checkST100
	case "ST101":
		return checkST101
	case "all":
		return allChecks
	}
	return 0
}

func parseChecks(entries []string) (checkSet, error) {
	if entries == nil {
		return allChecks, nil
	}
	var enabled checkSet
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		name, disable := strings.CutPrefix(entry, "-")
		checks := checkID(name)
		if checks == 0 {
			return 0, fmt.Errorf("checks: invalid entry %q; use all or ST001, ST002, ST003, ST100, ST101, optionally prefixed with '-'", entry)
		}
		if disable {
			enabled &^= checks
		} else {
			enabled |= checks
		}
	}
	return enabled, nil
}

// checkSelection implements flag.Value and owns its state independently of
// the caller's Options. Invalid programmatic options are reported by Run;
// invalid command-line values are rejected immediately by Set.
type checkSelection struct {
	entries []string
	enabled checkSet
	err     error
}

func newCheckSelection(options *Options) *checkSelection {
	entries := []string{"all"}
	if options != nil && options.Checks != nil {
		entries = append([]string{}, options.Checks...)
	}
	for i := range entries {
		entries[i] = strings.TrimSpace(entries[i])
	}
	enabled, err := parseChecks(entries)
	return &checkSelection{entries: entries, enabled: enabled, err: err}
}

func (s *checkSelection) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(s.entries, ",")
}

func (s *checkSelection) Set(value string) error {
	entries := []string{}
	if strings.TrimSpace(value) != "" {
		entries = strings.Split(value, ",")
		for i := range entries {
			entries[i] = strings.TrimSpace(entries[i])
		}
	}
	enabled, err := parseChecks(entries)
	if err != nil {
		return err
	}
	s.entries, s.enabled, s.err = entries, enabled, nil
	return nil
}
