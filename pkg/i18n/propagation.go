package i18n

import (
	"errors"
	"net/http"
)

// InjectHTTPHeaders propagates the negotiated language to an outbound HTTP request.
func InjectHTTPHeaders(req *http.Request) {
	if req == nil {
		return
	}
	decision, ok := LanguageFromContext(req.Context())
	if !ok || decision.Language == "" {
		return
	}
	req.Header.Set(HeaderOctoLang, decision.Language)
}

// PropagationRoundTripper injects X-Octo-Lang into outbound HTTP requests.
type PropagationRoundTripper struct {
	Base http.RoundTripper
}

// NewPropagationRoundTripper wraps base with language propagation.
func NewPropagationRoundTripper(base http.RoundTripper) *PropagationRoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &PropagationRoundTripper{Base: base}
}

func (t *PropagationRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("i18n: nil outbound HTTP request")
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	InjectHTTPHeaders(cloned)
	return t.base().RoundTrip(cloned)
}

func (t *PropagationRoundTripper) base() http.RoundTripper {
	if t == nil || t.Base == nil {
		return http.DefaultTransport
	}
	return t.Base
}
