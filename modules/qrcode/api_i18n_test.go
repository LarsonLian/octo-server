package qrcode

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

type qrErrEnvelope struct {
	Error struct {
		Code       string         `json:"code"`
		Details    map[string]any `json:"details"`
		HTTPStatus int            `json:"http_status"`
	} `json:"error"`
	Status int `json:"status"`
}

func TestQRCodeNoLegacyResponseError(t *testing.T) {
	files := []string{"api.go"}
	banned := []string{
		".ResponseError(",
		".ResponseErrorf(",
		".ResponseErrorWithStatus(",
		".AbortWithStatusJSON(",
		".AbortWithStatus(",
		".ResponseWithStatus(",
		"c.Response(\"",
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			cleaned := stripLineComments(string(data))
			for _, b := range banned {
				if strings.Contains(cleaned, b) {
					t.Fatalf("modules/qrcode/%s must use httperr.ResponseErrorL instead of legacy %s", f, b)
				}
			}
			for _, line := range strings.Split(cleaned, "\n") {
				if strings.Contains(line, "c.JSON(http.Status") && !strings.Contains(line, "c.JSON(http.StatusOK") {
					t.Fatalf("modules/qrcode/%s must not use raw non-OK c.JSON: %s", f, strings.TrimSpace(line))
				}
			}
		})
	}
}

func TestRespondQRCodeHelpers(t *testing.T) {
	cases := []struct {
		name       string
		probe      func(c *wkhttp.Context)
		wantCode   string
		wantHTTP   int
		wantDetail string
	}{
		{
			name:       "token_required",
			probe:      respondQRCodeTokenRequired,
			wantCode:   "err.server.qrcode.token_required",
			wantHTTP:   http.StatusBadRequest,
			wantDetail: "token",
		},
		{
			name:     "group_space_forbidden",
			probe:    func(c *wkhttp.Context) { respondQRCodeHandleError(c, errQRCodeGroupSpaceForbidden) },
			wantCode: "err.server.qrcode.group_space_forbidden",
			wantHTTP: http.StatusForbidden,
		},
		{
			name:     "wrapped_store_failed",
			probe:    func(c *wkhttp.Context) { respondQRCodeHandleError(c, errors.Join(errQRCodeInternalStoreFailed)) },
			wantCode: "err.server.qrcode.store_failed",
			wantHTTP: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := exerciseQRCodeHelper(t, tc.probe)
			if env.Status != http.StatusBadRequest {
				t.Fatalf("wire status = %d, want 400", env.Status)
			}
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.HTTPStatus != tc.wantHTTP {
				t.Fatalf("http_status = %d, want %d", env.Error.HTTPStatus, tc.wantHTTP)
			}
			if tc.wantDetail != "" && env.Error.Details["field"] != tc.wantDetail {
				t.Fatalf("field detail = %v, want %q", env.Error.Details["field"], tc.wantDetail)
			}
		})
	}
}

func exerciseQRCodeHelper(t *testing.T, probe func(c *wkhttp.Context)) qrErrEnvelope {
	t.Helper()
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.GET("/probe", probe)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	r.ServeHTTP(w, req)
	var env qrErrEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, w.Body.String())
	}
	return env
}

func stripLineComments(src string) string {
	var clean strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		clean.WriteString(line)
		clean.WriteByte('\n')
	}
	return clean.String()
}
