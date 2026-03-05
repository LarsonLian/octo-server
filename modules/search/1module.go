package search

import (
	_ "embed"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

//go:embed swagger/api.yaml
var swaggerContent string

func init() {

	register.AddModule(func(ctx interface{}) register.Module {

		return register.Module{
			Name: "search",
			SetupAPI: func() register.APIRouter {
				return New(ctx.(*config.Context))
			},
			Swagger: swaggerContent,
		}
	})
}
