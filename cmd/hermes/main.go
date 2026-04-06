package main

import (
	"runtime/debug"

	"github.com/nullun/hermes-vault-cli/cmd"
	"github.com/nullun/hermes-vault-cli/internal/logger"
)

var version = "dev"

func main() {
	defer func() {
		if r := recover(); r != nil {
			logger.LogPanic(r)
			panic(r)
		}
	}()
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		}
	}
	cmd.Execute(version)
}
