package client_test

import (
	"github.com/hackerwins/rottie/client"
	"github.com/hackerwins/rottie/rottie"
	"testing"
)

func withRottie(t *testing.T, f func(*testing.T, *rottie.Rottie)) {
	r, err := rottie.New()
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Start(); err != nil {
		t.Error(err)
	}

	f(t, r)

	if err := r.Shutdown(true); err != nil {
		t.Error(err)
	}
}

func TestRPCClient(t *testing.T) {
	withRottie(t, func(t *testing.T, r *rottie.Rottie) {
		t.Run("Can start and close", func(t *testing.T) {
			cli, err := client.NewRPCClient()
			if err != nil {
				t.Error(err)
			}

			defer func() {
				if err := cli.Close(); err != nil {
					t.Error(err)
				}
			}()

			if err := cli.Hello("world"); err != nil {
				t.Error(err)
			}
		})
	})
}