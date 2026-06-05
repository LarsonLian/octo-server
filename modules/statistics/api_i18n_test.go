package statistics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

func TestStatisticsNoLegacyResponseError(t *testing.T) {
	data, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatalf("read api.go: %v", err)
	}
	cleaned := stripStatisticsLineComments(string(data))
	for _, b := range []string{".ResponseError(", ".ResponseErrorf(", ".ResponseErrorWithStatus(", ".AbortWithStatusJSON(", ".AbortWithStatus(", ".ResponseWithStatus(", "c.Response(\""} {
		if strings.Contains(cleaned, b) {
			t.Fatalf("modules/statistics/api.go must use httperr.ResponseErrorL instead of legacy %s", b)
		}
	}
}

func TestRespondStatisticsHelpers(t *testing.T) {
	cases := []struct {
		name     string
		probe    func(c *wkhttp.Context)
		wantCode string
		wantHTTP int
	}{
		{
			name:     "request_invalid",
			probe:    func(c *wkhttp.Context) { respondStatisticsRequestInvalid(c, "date") },
			wantCode: "err.server.statistics.request_invalid",
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "query_failed",
			probe:    func(c *wkhttp.Context) { httperr.ResponseErrorL(c, errcode.ErrStatisticsQueryFailed, nil, nil) },
			wantCode: "err.server.statistics.query_failed",
			wantHTTP: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := exerciseStatisticsHelper(t, tc.probe)
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.HTTPStatus != tc.wantHTTP {
				t.Fatalf("http_status = %d, want %d", env.Error.HTTPStatus, tc.wantHTTP)
			}
		})
	}
}

type statisticsErrEnvelope struct {
	Error struct {
		Code       string `json:"code"`
		HTTPStatus int    `json:"http_status"`
	} `json:"error"`
}

func exerciseStatisticsHelper(t *testing.T, probe func(c *wkhttp.Context)) statisticsErrEnvelope {
	t.Helper()
	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.GET("/probe", probe)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	r.ServeHTTP(w, req)
	var env statisticsErrEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, w.Body.String())
	}
	return env
}

func stripStatisticsLineComments(src string) string {
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
