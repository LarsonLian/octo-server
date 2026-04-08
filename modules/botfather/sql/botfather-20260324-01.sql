-- +migrate Up
-- 修复历史数据：已软删除的 Bot（status=0）仍占用 username，
-- 导致相同标识符无法被 /newbot 复用。
-- 配合 PR #791 的增量修复（deleteRobot 时清空 username）。
-- 注意：robot 表由 robot 模块创建，按文件名排序可能晚于此脚本。
-- 对于全新数据库此操作为 no-op；对于已有数据库 robot 表必然已存在。

-- +migrate StatementBegin
CREATE PROCEDURE _bf_fix_username()
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'robot') THEN
    UPDATE `robot` SET `username` = '' WHERE `status` = 0 AND `username` != '';
  END IF;
END
-- +migrate StatementEnd

CALL _bf_fix_username();
DROP PROCEDURE IF EXISTS _bf_fix_username;

-- +migrate Down
-- 不可逆操作：已清空的 username 无法还原（原始值未保留）。
