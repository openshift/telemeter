package logger

import (
	"github.com/go-kit/kit/log/level"
)

// LogLevelFromString determines log level to string, defaults to all,
func LogLevelFromString(l string) level.Option {
	switch l {
	case "debug":
		return level.AllowDebug()
	case "info":
		return level.AllowInfo()
	case "warn":
		return level.AllowWarn()
	case "error":
		return level.AllowError()
	default:
		return level.AllowAll()
	}
}
