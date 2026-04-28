package category

import (
	"embed"

	spacemod "github.com/Mininglamp-OSS/octo-server/modules/space"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

//go:embed sql
var sqlFS embed.FS

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		api := New(ctx.(*config.Context))
		return register.Module{
			Name: "category",
			SetupAPI: func() register.APIRouter {
				return api
			},
			SQLDir: register.NewSQLFS(sqlFS),
		}
	})

	// 反向注入 provisioner：space 模块在创建空间 / 加入空间时回调此处保证默认分类存在。
	// 见 modules/space/hooks.go 说明 —— 避免 space → category 双向 import 形成 cycle。
	spacemod.RegisterDefaultCategoryProvisioner(EnsureDefaultCategory)
}
