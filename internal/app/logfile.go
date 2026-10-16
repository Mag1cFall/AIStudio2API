package app

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// logFileName 是日志目录中当前日志文件的名称
const logFileName = "aistudio2api.log"

// logOutput 把控制台日志写到标准错误与可开关的轮转日志文件
type logOutput struct {
	dir  string
	mu   sync.Mutex
	file *lumberjack.Logger
}

// set 开启或关闭日志文件；单文件按 config.LogFileMB 轮转，目录总量超过 maxTotalMB 时删除最旧的文件
func (output *logOutput) set(enabled bool, maxTotalMB int) error {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.file != nil {
		if err := output.file.Close(); err != nil {
			return err
		}
		output.file = nil
	}
	if !enabled {
		return nil
	}
	output.file = &lumberjack.Logger{
		Filename:   filepath.Join(output.dir, logFileName),
		MaxSize:    config.LogFileMB,
		MaxBackups: maxTotalMB/config.LogFileMB - 1,
		LocalTime:  true,
	}
	return nil
}

// Write 把一条日志写到标准错误与日志文件
func (output *logOutput) Write(data []byte) (int, error) {
	os.Stderr.Write(data)
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.file != nil {
		return output.file.Write(data)
	}
	return len(data), nil
}
