package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"example.com/sib/util"
)

func main() {
	switch os.Args[1] {
	case "modinfo":
		bi, _ := debug.ReadBuildInfo()
		fmt.Print(bi.String())
	case "trimpath":
		fmt.Println(util.SourcePath())
	}
}
