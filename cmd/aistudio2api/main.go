package main

import (
	"os"

	"github.com/Mag1cFall/AIStudio2API/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:]))
}
