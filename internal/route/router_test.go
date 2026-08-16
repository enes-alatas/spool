package route

import (
	"reflect"
	"testing"
)

func TestMentions(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"@ping 1", []string{"ping"}},
		{"hey @Fixer and @watcher-2, look", []string{"fixer", "watcher-2"}},
		{"emails like a@b.com don't count", nil},
		{"@dup @dup once", []string{"dup"}},
		{"(@paren) works", []string{"paren"}},
		{"none here", nil},
		{"@planner_spool_bot hello", []string{"planner_spool_bot"}},
	}
	for _, c := range cases {
		got := Mentions(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Mentions(%q) = %v want %v", c.in, got, c.want)
		}
	}
}
