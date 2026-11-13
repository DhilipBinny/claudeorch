package main

import "testing"

func TestMisplacedOwnFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--dangerously-skip-permissions"}, ""},
		{[]string{"--resume", "abc"}, ""},
		{[]string{"--force"}, "--force"},
		{[]string{"--isolated"}, "--isolated"},
		// Flags claude also defines must reach claude, not be rejected.
		{[]string{"--debug"}, ""},
		{[]string{"-p", "hi", "--json"}, ""},
		{[]string{"--cwd", "/tmp"}, ""},
		// Prefix lookalikes that belong to claude must pass through.
		{[]string{"--forceful"}, ""},
	}
	for _, c := range cases {
		if got := misplacedOwnFlag(c.args); got != c.want {
			t.Errorf("misplacedOwnFlag(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestSplitClaudeArgs(t *testing.T) {
	cases := []struct {
		in         []string
		wantClaude []string
		wantCheck  []string
	}{
		{nil, nil, nil},
		// "--" right after the profile name is consumed.
		{[]string{"--", "--resume", "x"}, []string{"--resume", "x"}, []string{}},
		// A later "--" belongs to claude and is kept.
		{[]string{"--print", "--", "--x"}, []string{"--print", "--", "--x"}, []string{"--print"}},
		// Flags after "--" are not checked.
		{[]string{"--", "--force"}, []string{"--force"}, []string{}},
		{[]string{"--resume", "x"}, []string{"--resume", "x"}, []string{"--resume", "x"}},
	}
	for _, c := range cases {
		gotClaude, gotCheck := splitClaudeArgs(c.in)
		if len(gotClaude) != len(c.wantClaude) || len(gotCheck) != len(c.wantCheck) {
			t.Errorf("splitClaudeArgs(%v) = %v, %v; want %v, %v", c.in, gotClaude, gotCheck, c.wantClaude, c.wantCheck)
			continue
		}
		for i := range gotClaude {
			if gotClaude[i] != c.wantClaude[i] {
				t.Errorf("splitClaudeArgs(%v) claude = %v, want %v", c.in, gotClaude, c.wantClaude)
			}
		}
	}
}
