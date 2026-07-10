package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/microck/moji/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit((app.App{}).Run(ctx, os.Args[1:]))
}
