package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.report.* — modules/report business error codes (api.go /
// api_manager.go).
var (
	ErrReportRequestInvalid = register(codes.Code{
		ID:             "err.server.report.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid report request.",
		SafeDetailKeys: []string{"field"},
	})
	ErrReportSessionInvalid = register(codes.Code{
		ID:             "err.server.report.session_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "The report session is invalid or has expired.",
	})
	ErrReportQueryFailed = register(codes.Code{
		ID:             "err.server.report.query_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to query report data.",
		Internal:       true,
	})
	ErrReportStoreFailed = register(codes.Code{
		ID:             "err.server.report.store_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to update report data.",
		Internal:       true,
	})
)
