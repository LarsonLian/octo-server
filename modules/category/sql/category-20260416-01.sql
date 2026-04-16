-- +migrate Up

-- 1. 清理重复的默认分组：迁移群聊关联到保留的分组，再删除多余行
-- +migrate StatementBegin
CREATE PROCEDURE _dedup_default_categories()
BEGIN
    -- 每个 (uid, space_id) 保留 id 最小的一条
    CREATE TEMPORARY TABLE _keep AS
        SELECT uid, space_id, MIN(id) AS keep_id
        FROM group_category
        WHERE is_default = 1 AND status = 1
        GROUP BY uid, space_id;

    -- 找出保留行的 category_id
    CREATE TEMPORARY TABLE _keep_cat AS
        SELECT k.uid, k.space_id, gc.category_id AS keep_category_id
        FROM _keep k
        INNER JOIN group_category gc ON gc.id = k.keep_id;

    -- 找出要删除的行
    CREATE TEMPORARY TABLE _remove AS
        SELECT gc.category_id AS remove_category_id, gc.uid, gc.space_id
        FROM group_category gc
        INNER JOIN _keep k ON k.uid = gc.uid AND k.space_id = gc.space_id
        WHERE gc.is_default = 1 AND gc.status = 1 AND gc.id != k.keep_id;

    -- 把挂在要删除分组下的群聊迁移到保留的分组
    UPDATE group_setting gs
    INNER JOIN _remove r ON gs.category_id = r.remove_category_id AND gs.uid = r.uid
    INNER JOIN _keep_cat kc ON kc.uid = r.uid AND kc.space_id = r.space_id
    SET gs.category_id = kc.keep_category_id;

    -- 标记重复行为已删除
    UPDATE group_category
    SET status = 2
    WHERE is_default = 1
      AND status = 1
      AND id NOT IN (SELECT keep_id FROM _keep);

    DROP TEMPORARY TABLE _keep;
    DROP TEMPORARY TABLE _keep_cat;
    DROP TEMPORARY TABLE _remove;
END;
-- +migrate StatementEnd

CALL _dedup_default_categories();
DROP PROCEDURE IF EXISTS _dedup_default_categories;

-- 2. is_default=0 改为 NULL，为唯一索引让路（MySQL 唯一索引不约束 NULL）
ALTER TABLE `group_category` MODIFY COLUMN `is_default` TINYINT DEFAULT NULL COMMENT '1=默认未分类分组（不可删除/重命名），NULL=普通分组';

UPDATE `group_category` SET `is_default` = NULL WHERE `is_default` = 0;

-- 3. 添加唯一索引：同一用户同一 Space 最多一个 is_default=1
ALTER TABLE `group_category` ADD UNIQUE KEY `uk_uid_space_is_default` (`uid`, `space_id`, `is_default`);
