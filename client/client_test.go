package client_test

import (
	"testing"

	"github.com/hackerwins/rottie/client"
	"github.com/hackerwins/rottie/rottie"
)

func TestClient(t *testing.T) {
	withRottie(t, func(t *testing.T, r *rottie.Rottie) {
		t.Run("start/close test", func(t *testing.T) {
			cli, err := client.NewClient(t.Name())
			if err != nil {
				t.Error(err)
			}

			defer func() {
				if err := cli.Close(); err != nil {
					t.Error(err)
				}
			}()
		})

		t.Run("activate/deactivate test", func(t *testing.T) {
			cli, err := client.NewClient(t.Name())
			if err != nil {
				t.Error(err)
			}

			defer func() {
				if err := cli.Close(); err != nil {
					t.Error(err)
				}
			}()

			if err := cli.Activate(); err != nil {
				t.Error(err)
			}
		})
	})
}

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
