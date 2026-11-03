package bubblelint

import (
	"bytes"
	"strings"
	"testing"
)

func TestChecks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    checkSet
	}{
		{"default", nil, allChecks},
		{"empty", []string{}, 0},
		{"all", []string{"all"}, allChecks},
		{"one", []string{"ST003"}, checkST003},
		{"multiple", []string{"ST001", "ST101"}, checkST001 | checkST101},
		{"exclude", []string{"all", "-ST101"}, allChecks &^ checkST101},
		{"re-enable", []string{"all", "-ST100", "ST100"}, allChecks},
		{"reset", []string{"all", "-all", "ST003"}, checkST003},
		{"disable-all", []string{"-all"}, 0},
		{"only-exclusion-starts-empty", []string{"-ST101"}, 0},
		{"whitespace", []string{" all ", " -ST101\t"}, allChecks &^ checkST101},
		{"duplicates", []string{"ST003", "ST003"}, checkST003},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := Options{Checks: tc.entries}
			if err := options.Validate(); err != nil {
				t.Fatal(err)
			}
			selection := newCheckSelection(&options)
			if selection.err != nil || selection.enabled != tc.want {
				t.Fatalf("got checks %b, error %v; want %b", selection.enabled, selection.err, tc.want)
			}
			fromFlag := newCheckSelection(nil)
			if err := fromFlag.Set(selection.String()); err != nil || fromFlag.enabled != tc.want {
				t.Fatalf("flag selection %q: got %b, error %v; want %b", selection.String(), fromFlag.enabled, err, tc.want)
			}
		})
	}
}

func TestInvalidChecks(t *testing.T) {
	for _, entry := range []string{"", " ", "ST999", "st001", "ST1*", "none", "-", "--ST001", "- ST001", "ST001,ST002"} {
		t.Run(entry, func(t *testing.T) {
			options := Options{Checks: []string{entry}}
			if err := options.Validate(); err == nil || !strings.Contains(err.Error(), "checks:") {
				t.Fatalf("expected checks error, got %v", err)
			}
			a := New(&options)
			if _, err := a.Run(nil); err == nil {
				t.Fatal("invalid programmatic options must fail before analysis")
			}
			// A valid flag override replaces even invalid initial Options.
			if err := a.Flags.Set("checks", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Run(nil); err != nil {
				t.Fatalf("empty selection should skip analysis: %v", err)
			}
		})
	}
}

func TestCheckFlagRejectsInvalidValueWithoutChangingSelection(t *testing.T) {
	selection := newCheckSelection(&Options{Checks: []string{"ST003"}})
	for _, value := range []string{"ST999", "ST001,,ST002", ",ST001", "ST001,", "all,-", "ST1*"} {
		if err := selection.Set(value); err == nil {
			t.Errorf("accepted invalid value %q", value)
		}
		if selection.enabled != checkST003 || selection.String() != "ST003" || selection.err != nil {
			t.Fatalf("invalid value %q changed the selection: %+v", value, selection)
		}
	}
}

func TestCheckOptionsAreCopied(t *testing.T) {
	options := Options{Checks: []string{"ST003"}}
	a := New(&options)
	options.Checks[0] = "ST999"
	selection := a.Flags.Lookup("checks").Value.(*checkSelection)
	if selection.enabled != checkST003 || selection.String() != "ST003" {
		t.Fatal("mutating caller options changed the analyzer")
	}
}

func TestChecksHelp(t *testing.T) {
	a := New(nil)
	var output bytes.Buffer
	a.Flags.SetOutput(&output)
	a.Flags.PrintDefaults()
	if got := output.String(); !strings.Contains(got, "-checks") || !strings.Contains(got, "default all") || strings.Contains(got, "panic") {
		t.Fatalf("unexpected checks help: %s", got)
	}
	for _, removed := range []string{"suggest", "check-misuse"} {
		if a.Flags.Lookup(removed) != nil {
			t.Errorf("legacy flag %q still exists", removed)
		}
	}
}
