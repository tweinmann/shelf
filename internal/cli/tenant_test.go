package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tenant is the example tenant repository, which carries the instructions for coding agents.
const tenant = "../../examples/tenant"

// shelfCommand finds a shelf command line in Markdown: in a code span or a code block.
var shelfCommand = regexp.MustCompile("(?m)(?:`|^\\s*)(shelf [a-z][^`\\n]*)")

// TestTenantAgentInstructions keeps the skill honest: every shelf command it tells an agent to
// run exists, with the flags it names.
func TestTenantAgentInstructions(t *testing.T) {
	t.Parallel()
	root := New(Options{})
	seen := 0
	for _, file := range []string{".claude/skills/shelf/SKILL.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(tenant, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range shelfCommand.FindAllStringSubmatch(string(data), -1) {
			// What a shelf command is piped into is not shelf's.
			command, _, _ := strings.Cut(m[1], " | ")
			fields := strings.Fields(command)[1:]
			cmd, rest, err := root.Find(fields)
			if err != nil || cmd == root {
				t.Errorf("%s: %q is not a shelf command", file, m[1])
				continue
			}
			for _, arg := range rest {
				name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
				if strings.HasPrefix(arg, "--") && cmd.Flags().Lookup(name) == nil {
					t.Errorf("%s: %q: shelf %s has no flag --%s", file, m[1], cmd.Name(), name)
				}
			}
			seen++
		}
	}
	if seen == 0 {
		t.Fatal("the instructions name no shelf command")
	}

	skill, _ := os.ReadFile(filepath.Join(tenant, ".claude/skills/shelf/SKILL.md"))
	for _, want := range []string{`--wait --commit "$(git rev-parse HEAD)" --json`, "| 0 |", "| 1 |", "| 2 |",
		"Make the app or a preview public", "in the background", "gh run watch --exit-status",
		"--preview <branch> --wait", "takes about a minute longer"} {
		if !strings.Contains(string(skill), want) {
			t.Errorf("the skill lacks %q", want)
		}
	}
	// A hook would block the agent's turn for as long as a build takes.
	for _, file := range []string{".claude/settings.json", ".claude/settings.local.json"} {
		if _, err := os.Stat(filepath.Join(tenant, file)); err == nil {
			t.Errorf("the tenant template ships %s", file)
		}
	}
}
