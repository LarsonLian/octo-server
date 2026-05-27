package i18n

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInjectHTTPHeadersPropagatesLanguage(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	ctx := WithLanguage(req.Context(), LanguageDecision{Language: "zh-CN", Source: LanguageSourceCookie})
	req = req.WithContext(ctx)

	InjectHTTPHeaders(req)

	if got := req.Header.Get(HeaderOctoLang); got != "zh-CN" {
		t.Fatalf("%s = %q, want zh-CN", HeaderOctoLang, got)
	}
}

func TestPropagationRoundTripperInjectsClonedRequest(t *testing.T) {
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get(HeaderOctoLang); got != "zh-CN" {
			t.Fatalf("%s = %q, want zh-CN", HeaderOctoLang, got)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	rt := NewPropagationRoundTripper(base)

	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req = req.WithContext(WithLanguage(req.Context(), LanguageDecision{
		Language: "zh-CN",
		Source:   LanguageSourceCookie,
	}))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("StatusCode = %d", resp.StatusCode)
	}
	if got := req.Header.Get(HeaderOctoLang); got != "" {
		t.Fatalf("original request header mutated: %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
