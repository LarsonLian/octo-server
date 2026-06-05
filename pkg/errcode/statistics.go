package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.statistics.* — modules/statistics business error codes (api.go).
var (
	ErrStatisticsRequestInvalid = register(codes.Code{
		ID:             "err.server.statistics.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid statistics request.",
		SafeDetailKeys: []string{"field"},
	})
	ErrStatisticsQueryFailed = register(codes.Code{
		ID:             "err.server.statistics.query_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to query statistics data.",
		Internal:       true,
	})
)
