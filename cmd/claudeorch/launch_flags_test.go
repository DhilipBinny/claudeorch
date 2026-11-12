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
		{[]string{"--cwd", "/tmp"}, "--cwd"},
		{[]string{"--cwd=/tmp"}, "--cwd"},
		{[]string{"--isolated"}, "--isolated"},
		{[]string{"-p", "hi", "--json"}, "--json"},
		// Prefix lookalikes that belong to claude must pass through.
		{[]string{"--forceful"}, ""},
	}
	for _, c := range cases {
		if got := misplacedOwnFlag(c.args); got != c.want {
			t.Errorf("misplacedOwnFlag(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}
