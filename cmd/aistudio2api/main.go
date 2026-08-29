package main

import (
	"os"

	"github.com/Mag1cFall/AIStudio2API/internal/app"
)

// main 执行 aistudio2api 命令入口
func main() {
	os.Exit(app.Run(os.Args[1:]))
}
