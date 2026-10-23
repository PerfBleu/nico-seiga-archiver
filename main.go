package main

import (
	"fmt"
	"os"

	"nico-seiga-archiver/internal/cli"
)

func main() {
	// 先处理 -c/--config：配置文件路径必须在读取配置之前确定。
	cfgPath, args := cli.ExtractConfigFlag(os.Args[1:])
	if cfgPath != "" {
		_ = os.Setenv("NICOSEIGA_CONFIG", cfgPath)
	}

	c, err := cli.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}
	os.Exit(c.Run(args))
}
