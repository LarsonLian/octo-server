package qrcode

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-server/modules/group"
	_ "github.com/Mininglamp-OSS/octo-server/modules/thread"
	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	key := make([]byte, 16)
	rand.Read(key)
	os.Setenv("OCTO_MASTER_KEY", hex.EncodeToString(key)) // 32 hex chars = 32 bytes
	os.Exit(m.Run())
}


func TestHandleJoinGroup_GroupNotFound(t *testing.T) {
	s, ctx := testutil.NewTestServer()

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	code := util.GenerUUID()
	err = ctx.GetRedisConn().SetAndExpire(
		fmt.Sprintf("%s%s", common.QRCodeCachePrefix, code),
		util.ToJson(common.NewQRCodeModel(common.QRCodeTypeGroup, map[string]interface{}{
			"group_no":  "non-existent-group",
			"generator": "10001",
		})),
		time.Minute,
	)
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/qrcode/"+code, nil)
	req.Header.Set("token", testutil.Token)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "群不存在")
}

func TestHandleJoinGroup_NotSpaceMember(t *testing.T) {
	s, ctx := testutil.NewTestServer()

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 插入群组（属于 space1），测试用户 testutil.UID 不在 space1
	groupDB := group.NewDB(ctx)
	err = groupDB.Insert(&group.Model{
		GroupNo: "group1",
		Name:    "测试群",
		Creator: "10001",
		Status:  1,
		SpaceID: "space1",
	})
	assert.NoError(t, err)

	code := util.GenerUUID()
	err = ctx.GetRedisConn().SetAndExpire(
		fmt.Sprintf("%s%s", common.QRCodeCachePrefix, code),
		util.ToJson(common.NewQRCodeModel(common.QRCodeTypeGroup, map[string]interface{}{
			"group_no":  "group1",
			"generator": "10001",
		})),
		time.Minute,
	)
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/qrcode/"+code, nil)
	req.Header.Set("token", testutil.Token)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请先加入该空间后再扫码入群")
}
