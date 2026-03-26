-- +migrate Up
-- robot_apply 表增加 space_id 字段，持久化申请来源 Space
ALTER TABLE `robot_apply` ADD COLUMN `space_id` VARCHAR(100) NOT NULL DEFAULT '' AFTER `remark`;

-- +migrate Down
ALTER TABLE `robot_apply` DROP COLUMN `space_id`;
