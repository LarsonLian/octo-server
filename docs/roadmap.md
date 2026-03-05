# DMWork V1 Roadmap

## v1.1 — 安全修复 + 核心增强 ✅ 已完成
- [x] Bot token 吊销安全修复
- [x] WuKongIM managerToken 认证（5300 端口限制 127.0.0.1）
- [x] Android 文件安全修复 (#21 路径遍历, #22 大小/类型限制)
- [x] robotList API 权限升级 (#36 → #37)
- [x] Web emoji 显示修复 (#14 → dmwork-web#18)
- [x] 仓库迁移到 dmwork-org 组织
- [x] OpenClaw adapter 升级到 0.2.17

## v1.1.1 — Bug 修复 + 体验优化（2026-03-04）
- [x] Android 忘记密码验证码无效修复（code_type 参数缺失）
- [x] Android 文件消息"未知消息"修复（WKFileContent 注册）
- [x] Android 文本文件内置预览（yaml/json/md/代码文件等）
- [x] Android 未知格式文件保存到下载目录
- [x] Android Logo 更新
- [x] 搜索用户支持邮箱查找
- [x] APK 下载地址统一到主域名
- [x] 团队协作流程规范文档
- [x] 组织成员邀请（lml2468/研发、yeejiaa/产品）

## v1.1.2 — Bot API + 工程化（2026-03-05）
- [x] Bot 历史消息拉取接口 /v1/bot/messages/sync (#50)
- [x] Bot skill.md 补全 5 个 API 文档 (#49)
- [x] Go 模块重命名 TangSengDaoDao → dmwork-org (#42)
- [x] dmwork-lib 公共核心库
- [x] dmwork-adapters CI 流水线
- [x] 部署脚本支持 server/web/adapter/all
- [x] CD 改为手动触发
- [x] npm 包名 V1/V2 分离
- [x] adapter 升级到 0.2.19
- [x] 开发流程增加 Issue 认领步骤
- [x] Jerry-Xin 加入 dev 团队

## v1.2 — 功能完善（2026 年 Q2）
- [ ] adapter prompt 国际化 + timestamp 标准化 (dmwork-adapters#9)
- [ ] 纯人类成员创建群聊 (dmwork-adapters#13)
- [ ] Bot 历史消息持久化（插件侧 SQLite/文件缓存）
- [ ] Android CI workflow
- [ ] 运营 Dashboard 认证（当前公开访问）
- [ ] 待根据 Issue 需求排期

## 长期方向
- V1 保持稳定运行，服务现有用户
- 核心功能持续迭代
- V2 (DeepIM) 并行开发，逐步替代
