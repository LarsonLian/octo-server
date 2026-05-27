package httperr

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
	"go.uber.org/zap"
)

// ResponseErrorL is the business-facing localized error facade.
//
// It validates the code, separates Params from Details, preserves the legacy
// HTTP/body status=400 compatibility path, and delegates translation/envelope
// rendering to the injected wkhttp.ErrorRenderer.
func ResponseErrorL(c *wkhttp.Context, code codes.Code, params i18n.Params, details i18n.Details) {
	if c == nil {
		return
	}

	registered, ok := codes.Lookup(code.ID)
	if !ok {
		log.Error("unregistered i18n error code", zap.String("code", code.ID), zap.String("path", c.FullPath()))
		registered, _ = codes.Lookup("err.shared.internal")
	}

	c.RenderError(wkhttp.ErrorSpec{
		Code:            registered.ID,
		DefaultMessage:  registered.DefaultMessage,
		TransportStatus: http.StatusBadRequest,
		SemanticStatus:  registered.HTTPStatus,
		Params:          params,
		Details:         details.FilterBy(registered),
		Internal:        registered.Internal,
	})
}
