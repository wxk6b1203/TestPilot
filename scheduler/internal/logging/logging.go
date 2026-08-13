// Package logging 统一 zap 日志入口。
package logging

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// L 是全局 SugaredLogger；Init 前为无操作的开发默认。
var L = zap.NewNop().Sugar()

// Init 初始化全局日志。
// level: debug/info/warn/error；dev 模式输出彩色行文本，否则 JSON。
func Init(level string) {
	lv, err := zapcore.ParseLevel(strings.ToLower(level))
	if err != nil {
		lv = zapcore.InfoLevel
	}
	dev := os.Getenv("TP_LOG_FORMAT") != "json"

	var cfg zap.Config
	if dev {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		cfg = zap.NewProductionConfig()
	}
	cfg.Level = zap.NewAtomicLevelAt(lv)

	logger, err := cfg.Build()
	if err != nil {
		panic("logging init: " + err.Error())
	}
	L = logger.Sugar()
	zap.ReplaceGlobals(logger)
}

// Sync 冲刷缓冲（main defer 调用）。
func Sync() {
	_ = L.Sync()
}
