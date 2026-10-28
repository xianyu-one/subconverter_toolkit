package main

import (
	"fmt"
	"os"

	"prefetch-proxy/internal/app"
)

func main() {
	var err error
	switch {
	case len(os.Args) == 1:
		app.Run()
	case len(os.Args) == 2 && os.Args[1] == "check":
		err = app.Check()
	case len(os.Args) == 2 && os.Args[1] == "reload":
		err = app.Reload()
	default:
		err = fmt.Errorf("用法: %s [check|reload]", os.Args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
