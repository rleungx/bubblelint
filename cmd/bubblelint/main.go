package main

import (
	"github.com/rleungx/bubblelint"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(bubblelint.New(nil))
}
