package cli

import (
	"reflect"
	"testing"
)

func TestResolveBinaryWith(t *testing.T) {
	lookPath := func(name string) (string, bool) {
		if name == "claude" {
			return "/opt/homebrew/bin/claude", true
		}
		return "", false
	}
	cases := []struct {
		name string
		argv []string
		want []string
	}{
		{"bare name resolves to absolute", []string{"claude", "--flag"}, []string{"/opt/homebrew/bin/claude", "--flag"}},
		{"absolute path is left untouched", []string{"/bin/zsh"}, []string{"/bin/zsh"}},
		{"unresolvable name is left as-is", []string{"nonesuch"}, []string{"nonesuch"}},
		{"empty argv is safe", nil, nil},
	}
	for _, c := range cases {
		if got := resolveBinaryWith(c.argv, lookPath); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: resolveBinaryWith(%v)=%v want %v", c.name, c.argv, got, c.want)
		}
	}
}
