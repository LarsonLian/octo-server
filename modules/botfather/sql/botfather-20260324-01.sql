-- +migrate Up
-- 修复历史数据：已软删除的 Bot（status=0）仍占用 username，
-- 导致相同标识符无法被 /newbot 复用。
-- 配合 PR #791 的增量修复（deleteRobot 时清空 username）。
UPDATE `robot` SET `username` = '' WHERE `status` = 0 AND `username` != '';

-- +migrate Down
-- 不可逆操作：已清空的 username 无法还原（原始值未保留）。
-- 如需回滚，需从备份恢复。
