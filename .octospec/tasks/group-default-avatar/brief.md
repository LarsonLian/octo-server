---
type: Task
title: "Task: group-default-avatar"
description: Server-rendered default group avatar (colored circle + group-name initials / group icon), replacing the legacy member-avatar composite, plus custom text+color persistence.
tags: ["group", "avatar", "wire-contract", "i18n", "default-avatar"]
timestamp: 2026-06-25T03:00:28Z
# --- octospec extension fields ---
slug: group-default-avatar
upstream: self
source: self
---

# Task: group-default-avatar

> One task = one `.octospec/tasks/<slug>/` directory. This brief is the spec for
> the work. AI may draft it from existing code; a human confirms it.

PRD「群聊与个人默认头像需求」的 **Module 2(群聊默认头像)** 后端实现。个人默认
头像已在 PR #346 上线(`feat(user): server-rendered default avatar`),本任务把
同一套「服务端出图」思路套到群头像:用「色块圆 + 群名前若干字 / 群组图标」取代
现有的「成员头像九宫格合成」。头像由 `GET /v1/groups/:group_no/avatar` 出图,
**所有客户端共用**,非单一 Web 端改动。

## Goal
群聊没有自定义头像时,`avatarGet` **服务端实时渲染**默认头像:色块圆 + 群名前 4
个可见字符(中文 ≤4 字 / 英文 ≤4 字符);取不出字时回退**群组图标**。颜色从现有
`pkg/avatarrender` 固定色板按 `groupNo` 稳定取色——同群跨页面一致、改名不变色、
改名时文字同步更新。群主可在「修改头像」二次弹窗自定义**头像文字 + 颜色**,二者
**落库**(`avatar_text` / `avatar_color`),渲染时覆盖默认派生值。群主上传的自定义
图片(`is_upload_avatar=1`)优先级最高,行为不变。历史群不批量刷库:无自定义头像
的历史群自动走新默认。

## Background
- **参考实现**:`modules/user/api.go` `UserAvatar`(`is_upload_avatar=0` 时用
  `avatarrender.IndividualText`+`Render`+`ColorForSeed` 出图 + 弱 ETag/304);
  共享渲染包 `pkg/avatarrender/`(`render.go`/`text.go`/`palette.go`)。
- **群头像现状**:`avatarGet`(`modules/group/api.go:353`)只读 `avatar_version`、
  **不看 `is_upload_avatar`**,一律 302 重定向到对象存储路径;系统群/`org_`/`dept_`
  走顶部静态图分支。默认头像来自**异步九宫格合成**:`handleGroupAvatarUpdateEvent`
  (`modules/base/event/handler.go:163`)→ `fileService.DownloadAndMakeCompose`
  拉取 ≤9 个成员头像拼图,`updateGeneratedGroupAvatar` 写 path+version(保持
  `is_upload_avatar=0`)。触发点:`group/api.go:1450 / 2199`、`service.go:1886`。
- **色板已确认**:对设计稿「群组头像颜色枚举」截图逐像素采样,10 色与现有
  `pkg/avatarrender/palette.go` 完全一致(`#14C0FF … #4954E6`,顺序一致)→ 直接复用。
- **二次弹窗**(设计稿 img_02):`自定义头像文字`(placeholder「最多显示4个中文/英文
  字符」)+ 10 色块选择器 + 实时预览。决定:**落库 + 服务端渲染**(非端上渲染上传)。
- **创建接口**:`groupCreate`(`api.go:685`)+ `groupReq`(`api.go:4035`);改群走
  `groupUpdate`(`api.go:844`)的 `groupUpdateActionMap`(`api_setting_action.go:284`)。
- **已锁定决定**:取群名**前 4** 可见 rune;自定义文字+颜色落库服务端渲染;复用现有
  色板;群组图标素材与精确色值**实现期先占位**,Figma 导出后替换。

**实现分增量(各自可独立评审/PR)**:
1. **本增量 — 创建接口 + 数据模型**:DB 迁移加 `avatar_text`/`avatar_color` 列;
   `group.Model` 加字段;`groupReq` 加 `avatar_text`/`avatar_color` 参数 +
   校验 + 落库;`pkg/avatarrender` 加 `GroupText`(前 4 字)+ `ColorByIndex`。
2. 后续 — `avatarGet` 服务端默认/自定义渲染分支 + ETag/304 + 群组图标占位。
3. 后续 — 改群 API(`avatar_text`/`avatar_color` 两个 update key)。
4. 后续 — 拆除九宫格合成事件链路。

## Load-bearing list
- **wire-contract**:`groupReq`(创建请求)新增 `avatar_text`/`avatar_color` 为
  **可选、向后兼容**字段(老客户端不传 → 走默认派生);`avatarGet` 出图契约后续
  增量从 302-重定向变为对默认头像 200-直出(与 user 端一致)。(rules: error-handling)
- **error-response / i18n**:新参数的非法值(`avatar_color` 越界、`avatar_text`
  超长/含不可见字符)必须走 `httperr.ResponseErrorL` + 注册的 `pkg/errcode` 码,
  禁止裸 `c.JSON`/`ResponseError`;`avatarGet` 二进制端点维持裸字节/状态码,不进
  i18n envelope。(rules: error-handling)
- **space / auth**:`groupCreate` 经 Space 校验、`avatarGet` 在 `openGroups`(免鉴权)
  分组——本任务**不改**鉴权/Space 隔离边界,新增字段不放宽任何 WHERE/可见性。
  (rules: space-isolation)
- **avatar 渲染一致性**:默认色 seed 必须用 `groupNo`(非群名),保证「改名不变色」
  「跨页面一致」;`is_upload_avatar=1` 永远优先,自定义图片行为不回归。
- **历史兼容**:已九宫格合成的群(`is_upload=0, version>0`)在新逻辑下自动改走服务端
  默认头像;不得批量刷库、不得改动已上传自定义头像的群。

## Out of scope
- **个人默认头像**(Module 3)——已上线,沿用历史逻辑,不动。
- **二次弹窗的「上传头像」**能力——沿用历史 `avatarUpload`,不改。
- `pkg/avatarrender` 字体文件 / `palette.go` 既有色值与顺序——复用,不改。
- `DownloadAndMakeCompose`/`MakeCompose` 函数本体——`file/api.go:77` 另有用途,保留。
- octo-lib 跨仓改动(`GetGroupAvatarFilePath` 等已就绪)。
- 群组图标**最终素材**与自定义色板**精确色值**——本期占位,Figma 导出后单独替换。
- 前端/客户端渲染与弹窗交互。

## Acceptance
本增量(创建接口 + 数据模型):
- `go test ./modules/group/... ./pkg/avatarrender/...` 通过。
- DB 迁移可重入(仿 `20260605000002_group_avatar_version.sql` 的
  INFORMATION_SCHEMA 守卫 + 存储过程),含 Up/Down。
- `groupCreate` 接收并落库 `avatar_text`/`avatar_color`;不传时写入默认哨兵
  (`avatar_text=''`、`avatar_color=-1`),老客户端请求行为不变。
- `avatar_color` 越界 / `avatar_text` 超长 经 `httperr.ResponseErrorL` + 注册码返回。
- `make i18n-extract-check` + `make i18n-lint` 通过(若新增 errcode)。
- 新增单测:`GroupText` 前 4 字截取(中/英/混排/含不可见字符/空)、`ColorByIndex`
  边界、`groupCreate` 落字段 round-trip。
- `golangci-lint run ./modules/group/... ./pkg/avatarrender/...` 通过。

整体功能(后续增量累计达成,列此供评审看全貌):
- 无自定义头像群:`avatarGet` 返回「色块圆 + 群名前 4 字」PNG;群名空/不可渲染 →
  群组图标。改名后文字变、颜色不变;同群跨页面同色。
- 自定义文字/颜色:渲染覆盖默认派生值;`is_upload_avatar=1` 优先级最高。
- 九宫格合成事件链路移除,历史群无需刷库即生效。
