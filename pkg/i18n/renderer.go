package i18n

import (
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
	"github.com/gin-gonic/gin"
)

const headerVary = "Vary"

// ErrorRenderer renders wkhttp.ErrorSpec through the octo-server i18n stack.
type ErrorRenderer struct {
	localizer Localizer
}

// NewErrorRenderer creates a wkhttp.ErrorRenderer implementation.
func NewErrorRenderer(localizer Localizer) *ErrorRenderer {
	if localizer == nil {
		localizer = NewLocalizer(SourceLanguage)
	}
	return &ErrorRenderer{localizer: localizer}
}

// Render emits the compatibility envelope unconditionally:
//   - error.{code,message,details,http_status} for new clients
//   - msg/status for legacy clients
func (r *ErrorRenderer) Render(c *wkhttp.Context, spec wkhttp.ErrorSpec) {
	if c == nil || c.Context == nil {
		return
	}

	lang := LanguageOrDefault(c.Request.Context(), SourceLanguage)
	if matched, ok := MatchSupportedLanguage(lang); ok {
		lang = matched
	} else {
		lang = SourceLanguage
	}

	transportStatus := normalizeHTTPStatus(spec.TransportStatus, http.StatusBadRequest)
	semanticStatus := normalizeHTTPStatus(spec.SemanticStatus, transportStatus)
	codeID := spec.Code
	if codeID == "" {
		codeID = "err.shared.internal"
	}

	msg := r.message(lang, spec)
	details := filteredDetails(spec)

	h := c.Writer.Header()
	h.Set("Content-Language", lang)
	addVary(h, "Accept-Language", HeaderOctoLang, "Cookie")

	c.JSON(transportStatus, gin.H{
		"error": gin.H{
			"code":        codeID,
			"message":     msg,
			"details":     details,
			"http_status": semanticStatus,
		},
		"msg":    msg,
		"status": transportStatus,
	})
}

func (r *ErrorRenderer) message(lang string, spec wkhttp.ErrorSpec) string {
	if spec.Internal {
		return r.localizer.Translate("err.shared.internal", lang, nil)
	}

	params := Params(spec.Params)
	msg := r.localizer.Translate(spec.Code, lang, params)
	if (msg == "" || msg == spec.Code) && spec.DefaultMessage != "" {
		if rendered, err := params.Render(spec.DefaultMessage); err == nil && rendered != "" {
			return rendered
		}
		return spec.DefaultMessage
	}
	return msg
}

func filteredDetails(spec wkhttp.ErrorSpec) Details {
	if spec.Internal {
		return Details{}
	}
	if len(spec.Details) == 0 {
		return Details{}
	}
	code, ok := codes.Lookup(spec.Code)
	if !ok {
		return Details{}
	}
	return Details(spec.Details).FilterBy(code)
}

func normalizeHTTPStatus(status, fallback int) int {
	if status >= 100 && status <= 599 {
		return status
	}
	return fallback
}

func addVary(h http.Header, fields ...string) {
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || varyContains(h, field) {
			continue
		}
		h.Add(headerVary, field)
	}
}

func varyContains(h http.Header, target string) bool {
	for _, value := range h.Values(headerVary) {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), target) {
				return true
			}
		}
	}
	return false
}
