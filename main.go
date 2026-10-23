package main

import (
	"fmt"
	"os"

	"nico-seiga-archiver/internal/cli"
)

func main() {
	c, err := cli.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}
	os.Exit(c.Run(os.Args[1:]))
}
