package openlistcore

import "strings"

// 保留已有 DSN 参数，由各平台提供其 SQLite 驱动支持的参数格式。
func appendDSNParams(dsn, params string) string {
	if params == "" {
		return dsn
	}
	switch {
	case strings.HasSuffix(dsn, "?"), strings.HasSuffix(dsn, "&"):
		return dsn + params
	case strings.Contains(dsn, "?"):
		return dsn + "&" + params
	default:
		return dsn + "?" + params
	}
}
