package statistics

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		x := ctx.(*config.Context)
		return register.Module{
			Name: "statistics",
			SetupAPI: func() register.APIRouter {
				return NewStatistics(x)
			},
		}
	})
}
