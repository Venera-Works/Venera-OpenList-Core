//go:build !android

package openlistcore

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openSQLite(dsn string) gorm.Dialector {
	// 纯 Go 驱动只识别 _pragma，不能使用 mattn 的 _journal 参数。
	return sqlite.Open(appendDSNParams(dsn,
		"_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"))
}
