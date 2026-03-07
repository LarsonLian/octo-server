package channel

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/db"
	"github.com/gocraft/dbr/v2"
)

type channelSettingDB struct {
	session *dbr.Session
	ctx     *config.Context
}

func newChannelSettingDB(ctx *config.Context) *channelSettingDB {
	return &channelSettingDB{
		session: ctx.DB(),
		ctx:     ctx,
	}
}

func (c *channelSettingDB) queryWithChannel(channelID string, channelType uint8) (*channelSettingModel, error) {
	var m *channelSettingModel
	_, err := c.session.Select("*").From("channel_setting").Where("channel_id=? and channel_type=?", channelID, channelType).Load(&m)
	return m, err
}

func (c *channelSettingDB) queryWithChannelIDs(channelIDs []string) ([]*channelSettingModel, error) {
	var models []*channelSettingModel
	_, err := c.session.Select("*").From("channel_setting").Where("channel_id in ?", channelIDs).Load(&models)
	return models, err
}

func (c *channelSettingDB) insertOrAddMsgAutoDelete(channelID string, channelType uint8, msgAutoDelete int64) error {
	tx, err := c.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	_, err = tx.InsertBySql("INSERT INTO channel_setting (channel_id, channel_type) VALUES (?, ?) ON DUPLICATE KEY UPDATE channel_id=channel_id", channelID, channelType).Exec()
	if err != nil {
		return err
	}
	_, err = tx.UpdateBySql("UPDATE channel_setting SET msg_auto_delete=? WHERE channel_id=? AND channel_type=?", msgAutoDelete, channelID, channelType).Exec()
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (c *channelSettingDB) insertOrAddOffsetMessageSeq(channelID string, channelType uint8, offsetMessageSeq uint32) error {
	tx, err := c.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	_, err = tx.InsertBySql("INSERT INTO channel_setting (channel_id, channel_type) VALUES (?, ?) ON DUPLICATE KEY UPDATE channel_id=channel_id", channelID, channelType).Exec()
	if err != nil {
		return err
	}
	_, err = tx.UpdateBySql("UPDATE channel_setting SET offset_message_seq=? WHERE channel_id=? AND channel_type=?", offsetMessageSeq, channelID, channelType).Exec()
	if err != nil {
		return err
	}
	return tx.Commit()
}

type channelSettingModel struct {
	ChannelID         string
	ChannelType       uint8
	ParentChannelID   string
	ParentChannelType uint8
	MsgAutoDelete     int64
	OffsetMessageSeq  uint32
	db.BaseModel
}
