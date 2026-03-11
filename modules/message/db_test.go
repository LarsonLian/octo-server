package message

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/stretchr/testify/assert"
)

func TestGetTable_ZeroMessageTableCount(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.TablePartitionConfig.MessageTableCount = 0
	ctx := config.NewContext(cfg)

	db := NewDB(ctx)

	// Should not panic and should return default table
	tableName := db.getTable("test-channel-id")
	assert.Equal(t, "message", tableName)
}

func TestGetTable_NegativeMessageTableCount(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.TablePartitionConfig.MessageTableCount = -1
	ctx := config.NewContext(cfg)

	db := NewDB(ctx)

	// Should not panic and should return default table
	tableName := db.getTable("test-channel-id")
	assert.Equal(t, "message", tableName)
}

func TestGetTable_ValidMessageTableCount(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.TablePartitionConfig.MessageTableCount = 4
	ctx := config.NewContext(cfg)

	db := NewDB(ctx)

	// Should return a valid table name based on hash
	tableName := db.getTable("test-channel-id")
	assert.Contains(t, []string{"message", "message1", "message2", "message3"}, tableName)
}
