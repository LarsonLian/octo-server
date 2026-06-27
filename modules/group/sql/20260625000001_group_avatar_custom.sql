-- +migrate Up
-- 群默认头像的自定义文字与颜色（PRD 群聊默认头像 Module 1 二次弹窗）。
--   avatar_text  : 自定义头像文字，'' 表示未自定义（渲染时回退群名前 4 字）。
--   avatar_color : 自定义色板下标 [0, palette)，NULL 表示未自定义（渲染时回退
--                  ColorForSeed(group_no)）。NULL 哨兵而非 0，是因为 0 是合法下标，
--                  且 dbr Record() 会写入结构体所有字段，*int=nil → NULL 让既有
--                  建群路径无需逐处显式赋值即可落到“未自定义”。
-- 使用 INFORMATION_SCHEMA 守卫保证迁移在部分执行后重试时仍可重入。
-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_avatar_custom;
-- +migrate StatementEnd

-- +migrate StatementBegin
CREATE PROCEDURE __group_avatar_custom()
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND COLUMN_NAME = 'avatar_text') THEN
    ALTER TABLE `group`
      ADD COLUMN `avatar_text` VARCHAR(16) NOT NULL DEFAULT '' COMMENT '自定义群头像文字，空表示用群名派生';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND COLUMN_NAME = 'avatar_color') THEN
    ALTER TABLE `group`
      ADD COLUMN `avatar_color` TINYINT DEFAULT NULL COMMENT '自定义群头像色板下标，NULL 表示按 group_no 派生';
  END IF;
END;
-- +migrate StatementEnd

CALL __group_avatar_custom();

-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_avatar_custom;
-- +migrate StatementEnd

-- +migrate Down
-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_avatar_custom_down;
-- +migrate StatementEnd

-- +migrate StatementBegin
CREATE PROCEDURE __group_avatar_custom_down()
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND COLUMN_NAME = 'avatar_color') THEN
    ALTER TABLE `group` DROP COLUMN `avatar_color`;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND COLUMN_NAME = 'avatar_text') THEN
    ALTER TABLE `group` DROP COLUMN `avatar_text`;
  END IF;
END;
-- +migrate StatementEnd

CALL __group_avatar_custom_down();

-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_avatar_custom_down;
-- +migrate StatementEnd
