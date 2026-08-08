package main

import (
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/server"
)

func TestWantsHelp(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		if !wantsHelp(args) {
			t.Errorf("wantsHelp(%v) = false, want true", args)
		}
	}
	for _, args := range [][]string{{}, {"serve"}, {"jobs"}, {"serve", "--help"}} {
		if wantsHelp(args) {
			t.Errorf("wantsHelp(%v) = true, want false", args)
		}
	}
}

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want server.Role
	}{
		{[]string{"serve"}, server.RoleServe},
		{[]string{"jobs"}, server.RoleJobs},
	} {
		got, err := parseArgs(tc.args)
		if err != nil {
			t.Fatalf("parseArgs(%v): %v", tc.args, err)
		}
		if got != tc.want {
			t.Errorf("parseArgs(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}

	for _, args := range [][]string{{}, {"serve", "jobs"}, {"nope"}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) = nil error, want error", args)
		}
	}
}

// The usage line is what `--help` prints, so it must name both subcommands.
func TestUsageNamesBothSubcommands(t *testing.T) {
	for _, role := range []server.Role{server.RoleServe, server.RoleJobs} {
		if !strings.Contains(usage, string(role)) {
			t.Errorf("usage %q does not mention %q", usage, role)
		}
	}
}
