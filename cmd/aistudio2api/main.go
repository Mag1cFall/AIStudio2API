package main

import (
	"os"

	"github.com/Mag1cFall/AIStudio2API/internal/app"
	_ "golang.org/x/crypto/x509roots/fallback" // 系统没有根证书时使用的 Mozilla 根证书
)

// main 执行 aistudio2api 命令入口
func main() {
	os.Exit(app.Run(os.Args[1:]))
}
