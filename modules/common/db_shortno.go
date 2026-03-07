package common

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"fmt"
	"os"
	"runtime/debug"
	dbs "github.com/Mininglamp-OSS/octo-lib/pkg/db"
	"github.com/gocraft/dbr/v2"
)

type shortnoDB struct {
	ctx *config.Context
	db  *dbr.Session
}

func newShortnoDB(ctx *config.Context) *shortnoDB {
	return &shortnoDB{
		ctx: ctx,
		db:  ctx.DB(),
	}
}

func (s *shortnoDB) inserts(shortnos []string) error {
	if len(shortnos) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err := recover(); err != nil {
			tx.RollbackUnlessCommitted()
			fmt.Fprintf(os.Stderr, "recovered panic in goroutine: %v\n%s\n", err, debug.Stack())
		}
	}()
	for _, st := range shortnos {
		_, err := tx.InsertBySql("insert into shortno(shortno) values(?)", st).Exec()
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return err
	}
	return nil

}

func (s *shortnoDB) queryVail() (*shortnoModel, error) {
	var m *shortnoModel
	_, err := s.db.Select("*").From("shortno").Where("used=0 and hold=0 and locked=0").Limit(1).Load(&m)
	return m, err
}

func (s *shortnoDB) updateLock(shortno string, lock int) error {
	_, err := s.db.Update("shortno").Set("locked", lock).Where("shortno=?", shortno).Exec()
	return err
}

// allocateShortnoAtomic atomically allocates a shortno using database-level locking.
// This is safe for multi-instance deployments unlike the in-memory mutex approach.
func (s *shortnoDB) allocateShortnoAtomic() (*shortnoModel, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.RollbackUnlessCommitted()

	// Atomically lock one available shortno using SELECT ... FOR UPDATE
	var m *shortnoModel
	_, err = tx.SelectBySql("SELECT * FROM shortno WHERE used=0 AND hold=0 AND locked=0 LIMIT 1 FOR UPDATE").Load(&m)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}

	// Mark it as locked within the same transaction
	_, err = tx.UpdateBySql("UPDATE shortno SET locked=1 WHERE shortno=?", m.Shortno).Exec()
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *shortnoDB) updateUsed(shortno string, used int, business string) error {
	_, err := s.db.Update("shortno").Set("used", used).Set("business", business).Where("shortno=?", shortno).Exec()
	return err
}
func (s *shortnoDB) updateHold(shortno string, hold int) error {
	_, err := s.db.Update("shortno").Set("hold", hold).Where("shortno=?", shortno).Exec()
	return err
}

func (s *shortnoDB) queryVailCount() (int64, error) {
	var cn int64
	_, err := s.db.Select("count(*)").From("shortno").Where("used=0 and hold=0 and locked=0").Load(&cn)
	return cn, err
}

type shortnoModel struct {
	Shortno  string
	Used     int
	Hold     int
	Locked   int
	Business string
	dbs.BaseModel
}
