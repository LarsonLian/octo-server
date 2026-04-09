package category

func (d *categoryDB) queryGroupSettingForCategory(groupNo, uid string) (*groupSettingCategoryRow, error) {
	var row *groupSettingCategoryRow
	_, err := d.session.Select("id", "group_no", "uid", "category_id", "category_sort").
		From("group_setting").
		Where("group_no=? and uid=?", groupNo, uid).
		Load(&row)
	return row, err
}

func (d *categoryDB) insertGroupSettingForCategory(groupNo, uid string, categoryID *string, categorySort int, version int64) error {
	_, err := d.session.InsertBySql(
		"INSERT INTO group_setting (group_no, uid, category_id, category_sort, revoke_remind, screenshot, receipt, version) VALUES (?, ?, ?, ?, 1, 1, 1, ?)",
		groupNo, uid, categoryID, categorySort, version,
	).Exec()
	return err
}

func (d *categoryDB) updateGroupSettingCategory(id int64, categoryID *string, categorySort int) error {
	_, err := d.session.Update("group_setting").
		Set("category_id", categoryID).
		Set("category_sort", categorySort).
		Where("id=?", id).
		Exec()
	return err
}

func (d *categoryDB) queryUserGroupsInSpace(uid, spaceID string) ([]*userGroupInfo, error) {
	var results []*userGroupInfo
	_, err := d.session.SelectBySql(`
		SELECT g.group_no, g.name as group_name,
			gs.category_id, IFNULL(gs.category_sort, 0) as category_sort
		FROM `+"`group`"+` g
		INNER JOIN group_member gm ON g.group_no = gm.group_no
		LEFT JOIN group_setting gs ON g.group_no = gs.group_no AND gs.uid = ?
		WHERE gm.uid = ? AND gm.is_deleted = 0 AND g.space_id = ?
		GROUP BY g.group_no
		ORDER BY gs.category_sort ASC
	`, uid, uid, spaceID).Load(&results)
	return results, err
}
