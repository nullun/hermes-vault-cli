package main

import (
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
	cmd.Execute(version)
}
