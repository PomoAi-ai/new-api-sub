package channels

import "testing"

func TestCheckBaseURL(t *testing.T) {
	ok := []string{"https://api.example.com", "https://api.example.com/v1", "https://8.8.8.8", "https://[2001:4860::8888]/v1"}
	bad := []string{
		"http://api.example.com", "https://", "api.example.com",
		"https://user:pw@api.example.com", "https://api.example.com/?x=1", "https://api.example.com/?",
		"https://api.example.com/#f", "https://10.0.0.1", "https://192.168.1.1:8443", "https://127.0.0.1",
		"https://169.254.169.254/latest", "https://0.0.0.0", "https://[::1]", "https://[fe80::1]",
		"https://[fd00::1]", "https://[::ffff:127.0.0.1]",
	}
	for _, u := range ok {
		if err := checkBaseURL(u); err != nil {
			t.Errorf("%s rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if checkBaseURL(u) == nil {
			t.Errorf("%s accepted", u)
		}
	}
}

func TestCleanKeyTrimsLinesAndDropsEmptyOnes(t *testing.T) {
	cases := map[string]string{
		"sk-1":                 "sk-1",
		"  sk-1 \r\n\n sk-2\n": "sk-1\nsk-2",
		" \n\t\r\n":            "",
		// a JSON key spanning lines stays valid JSON
		"{\n  \"access_token\": \"a\",\n  \"account_id\": \"b\"\n}\n": "{\n\"access_token\": \"a\",\n\"account_id\": \"b\"\n}",
	}
	for in, want := range cases {
		if got := CleanKey(in); got != want {
			t.Errorf("CleanKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickModelsAcceptsOnlyASubsetOfTheTemplate(t *testing.T) {
	const template = " a, b ,c,a"
	for requested, want := range map[string]string{
		"a":       "a",
		" c , a ": "a,c",
		"c,a,c,b": "a,b,c",
		"b,,b":    "b",
	} {
		if got, err := pickModels(template, requested); err != nil || got != want {
			t.Errorf("pickModels(%q) = %q, %v; want %q", requested, got, err, want)
		}
	}
	for _, requested := range []string{"", " , ", "x", "a,x", "A", "a b"} {
		if got, err := pickModels(template, requested); err == nil {
			t.Errorf("pickModels(%q) = %q, want an error", requested, got)
		}
	}
}
