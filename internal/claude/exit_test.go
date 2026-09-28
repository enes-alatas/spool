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
	for _, testCase := range cases {
		if got := IsPromptTooLong(testCase.res); got != testCase.want {
			t.Errorf("%s: IsPromptTooLong = %v, want %v", testCase.name, got, testCase.want)
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
	for _, testCase := range cases {
		if got := IsUnrecognizedModel(testCase.res); got != testCase.want {
			t.Errorf("%s: IsUnrecognizedModel = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestIsLoginRejected(t *testing.T) {
	// the stand-in message the CLI printed for an expired login (#405),
	// trimmed to the fields that matter
	rejected := `{"type":"assistant","message":{"model":"<synthetic>","role":"assistant",` +
		`"content":[{"type":"text","text":"Failed to authenticate: OAuth session expired and could not be refreshed"}],` +
		`"usage":{"input_tokens":0,"output_tokens":0}},"error":"authentication_failed","is_api_error_message":true}`
	quoted := `{"type":"assistant","message":{"role":"assistant",` +
		`"content":[{"type":"text","text":"the CLI says authentication_failed when a login expires"}],` +
		`"usage":{"input_tokens":12,"output_tokens":9}}}`
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"an expired login", rejected, true},
		{"a reply that quotes the error", quoted, false},
	}
	for _, testCase := range cases {
		msg := DecodeEvent([]byte(testCase.line)).Assistant
		if got := IsLoginRejected(msg); got != testCase.want {
			t.Errorf("%s: IsLoginRejected = %v, want %v", testCase.name, got, testCase.want)
		}
	}
	if IsLoginRejected(nil) {
		t.Error("no message at all: IsLoginRejected = true")
	}
}
