//go:build android

package openlistcore

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openSQLite(dsn string) gorm.Dialector {
	return sqlite.Open(appendDSNParams(dsn,
		"_journal=WAL&_vacuum=incremental&_busy_timeout=10000&_synchronous=NORMAL"))
}
