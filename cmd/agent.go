package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hackerwins/rottie/pkg/log"
	"github.com/hackerwins/rottie/rottie"
)

const gracefulTimeout = 10 * time.Second

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent [options]",
		Short: "Starts rottie agent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := rottie.New()
			if err != nil {
				return err
			}

			if err := r.Start(); err != nil {
				return err
			}

			if code := handleSignal(r); code != 0 {
				return fmt.Errorf("exit code: %d", code)
			}

			return nil
		},
	}
}

func handleSignal(r *rottie.Rottie) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var sig os.Signal
	select {
	case s := <-sigCh:
		sig = s
	case <-r.ShutdownCh():
		// rottie is already shutdown
		return 0
	}

	graceful := false
	if sig == syscall.SIGINT || sig == syscall.SIGTERM {
		graceful = true
	}

	log.Logger.Infof("Caught signal: %s", sig.String())

	gracefulCh := make(chan struct{})
	go func() {
		if err := r.Shutdown(graceful); err != nil {
			log.Logger.Error(err)
			return
		}
		close(gracefulCh)
	}()

	select {
	case <-sigCh:
		return 1
	case <-time.After(gracefulTimeout):
		return 1
	case <-gracefulCh:
		return 0
	}
}

func init() {
	rootCmd.AddCommand(newAgentCmd())
}
