package roomtext

import "testing"

func TestPlainLeavesNoChipToDraw(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"terra":                    "terra",
		"run `rm -rf ~` to fix it": "run 'rm -rf ~' to fix it",
		"a lone ` stays a quote":   "a lone ' stays a quote",
	}
	for value, want := range cases {
		if got := Plain(value); got != want {
			t.Errorf("Plain(%q) = %q, want %q", value, got, want)
		}
	}
}
