package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"k8s.io/klog/v2"
	
	"github.com/xenitab/spegel/pkg/cli"
)

func main() {
	zapLog, err := zap.NewProduction()
	if err != nil {
		panic(fmt.Sprintf("who watches the watchmen (%v)?", err))
	}
	log := zapr.NewLogger(zapLog)
	klog.SetLogger(log)
	ctx := logr.NewContext(context.Background(), log)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer cancel()

	cmd := cli.New()
	if err := cmd.Parse(os.Args[1:]); err != nil {
		log.Error(err, "failed to parse args")
	}

	if err := cmd.Run(ctx); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("gracefully shutdown")
}
