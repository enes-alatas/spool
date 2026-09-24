package claude

import "testing"

func TestIsPromptTooLong(t *testing.T) {
	cases := []struct {
		name string
		res  *ResultInfo
		want bool
	}{
		{"the signature the CLI returns", &ResultInfo{IsError: true, ResultText: "Prompt is too long"}, true},
		{"wrapped in a longer sentence", &ResultInfo{IsError: true, ResultText: "API Error: prompt is too long: 251000 tokens > 200000 maximum"}, true},
		{"a different failure", &ResultInfo{IsError: true, ResultText: "Invalid API key"}, false},
		{"a failed turn that merely quotes the phrase", &ResultInfo{IsError: true, ResultText: "Prompt is too long", Usage: Usage{InputTokens: 12, OutputTokens: 340}}, false},
		{"billed only for the cache read", &ResultInfo{IsError: true, ResultText: "Prompt is too long", Usage: Usage{CacheReadTokens: 9}}, false},
		{"the words without the failure", &ResultInfo{ResultText: "Prompt is too long"}, false},
		{"an ordinary reply", &ResultInfo{ResultText: "ok"}, false},
		{"no result at all", nil, false},
	}
	for _, c := range cases {
		if got := IsPromptTooLong(c.res); got != c.want {
			t.Errorf("%s: IsPromptTooLong = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsUnrecognizedModel(t *testing.T) {
	cases := []struct {
		name string
		res  *ResultInfo
		want bool
	}{
		{"the API's 404 on the messages call", &ResultInfo{IsError: true, APIErrorStatus: 404,
			ResultText: "There's an issue with the selected model (claude-nosuch-9)."}, true},
		{"another API failure", &ResultInfo{IsError: true, APIErrorStatus: 529, ResultText: "Overloaded"}, false},
		{"a failure with no API call", &ResultInfo{IsError: true, ResultText: "Prompt is too long"}, false},
		{"an ordinary reply", &ResultInfo{ResultText: "ok"}, false},
		{"no result at all", nil, false},
	}
	for _, c := range cases {
		if got := IsUnrecognizedModel(c.res); got != c.want {
			t.Errorf("%s: IsUnrecognizedModel = %v, want %v", c.name, got, c.want)
		}
	}
}
