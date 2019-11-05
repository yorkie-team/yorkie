package client_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hackerwins/rottie/client"
	"github.com/hackerwins/rottie/pkg/document"
	"github.com/hackerwins/rottie/pkg/document/proxy"
	"github.com/hackerwins/rottie/rottie"
	"github.com/hackerwins/rottie/testhelper"
)

const (
	testRPCAddr    = "localhost:1101"
	testCollection = "test-col"
)

func TestClient(t *testing.T) {
	testhelper.WithRottie(t, func(t *testing.T, r *rottie.Rottie) {
		t.Run("start/close test", func(t *testing.T) {
			cli, err := client.NewClient(testRPCAddr)
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
			cli, err := client.NewClient(testRPCAddr)
			if err != nil {
				t.Fatal(err)
			}

			defer func() {
				if err := cli.Close(); err != nil {
					t.Error(err)
				}
			}()

			ctx := context.Background()
			if err := cli.Activate(ctx); err != nil {
				t.Error(err)
			}

			// Already activated
			if err := cli.Activate(ctx); err != nil {
				t.Error(err)
			}

			if err := cli.Deactivate(ctx); err != nil {
				t.Error(err)
			}

			// Already deactivated
			if err := cli.Deactivate(ctx); err != nil {
				t.Error(err)
			}
		})
	})
}

func TestClientAndDocument(t *testing.T) {
	withRottieAndClient(t, func(t *testing.T, r *rottie.Rottie, c *client.Client) {
		t.Run("attach/detach test", func(t *testing.T) {
			ctx := context.Background()
			doc := document.New(testCollection, t.Name())
			if err := doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("k1", "k1")
				return nil
			}, "update k1 with k1"); err != nil {
				t.Error(err)
			}

			if err := c.AttachDocument(ctx, doc); err != nil {
				t.Error(err)
			}

			if err := c.Deactivate(ctx); err != nil {
				t.Error(err)
			}
		})
	})

	withRottieAndTwoClients(t, func(t *testing.T, r *rottie.Rottie, c1 *client.Client, c2 *client.Client) {
		t.Run("concurrent set test", func(t *testing.T) {
			ctx1 := context.Background()
			doc1 := document.New(testCollection, t.Name())
			if err := c1.AttachDocument(ctx1, doc1); err != nil {
				t.Error(err)
			}

			ctx2 := context.Background()
			doc2 := document.New(testCollection, t.Name())
			if err := c2.AttachDocument(ctx2, doc2); err != nil {
				t.Error(err)
			}

			if err := doc1.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("k1", "v1")
				return nil
			}, "update k1 by c1"); err != nil {
				t.Error(err)
			}

			if err := doc2.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("k1", "v2")
				return nil
			}, "update k1 by c2"); err != nil {
				t.Error(err)
			}

			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}

			if err := c2.PushPull(ctx2); err != nil {
				t.Error(err)
			}

			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}

			assert.Equal(t, doc1.Marshal(), doc2.Marshal())
		})

		t.Run("concurrent add test", func(t *testing.T) {
			ctx1 := context.Background()
			doc1 := document.New(testCollection, t.Name())
			if err := c1.AttachDocument(ctx1, doc1); err != nil {
				t.Error(err)
			}
			if err := doc1.Update(func(root *proxy.ObjectProxy) error {
				root.SetNewArray("k1").AddString("v1")
				return nil
			}, "new array and add v1"); err != nil {
				t.Error(err)
			}
			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}

			ctx2 := context.Background()
			doc2 := document.New(testCollection, t.Name())
			if err := c2.AttachDocument(ctx2, doc2); err != nil {
				t.Error(err)
			}

			if err := doc1.Update(func(root *proxy.ObjectProxy) error {
				root.GetArray("k1").AddString("v2")
				return nil
			}, "add v2 by c1"); err != nil {
				t.Error(err)
			}

			if err := doc2.Update(func(root *proxy.ObjectProxy) error {
				root.GetArray("k1").AddString("v3")
				return nil
			}, "add v3 by c2"); err != nil {
				t.Error(err)
			}

			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}
			if err := c2.PushPull(ctx2); err != nil {
				t.Error(err)
			}
			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}

			assert.Equal(t, doc1.Marshal(), doc2.Marshal())
		})
	})
}

func withRottieAndClient(
	t *testing.T,
	f func(t *testing.T, r *rottie.Rottie, c *client.Client),
) {
	testhelper.WithRottie(t, func(t *testing.T, r *rottie.Rottie) {
		c, err := client.NewClient(testRPCAddr)
		if err != nil {
			t.Fatal(err)
		}

		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()

		if err := c.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}

		f(t, r, c)
	})
}

func withRottieAndTwoClients(
	t *testing.T,
	f func(t *testing.T, r *rottie.Rottie, c1 *client.Client, c2 *client.Client),
) {
	testhelper.WithRottie(t, func(t *testing.T, r *rottie.Rottie) {
		c1, err := client.NewClient(testRPCAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := c1.Close(); err != nil {
				t.Error(err)
			}
		}()
		if err := c1.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := c1.Deactivate(context.Background()); err != nil {
				t.Error(err)
			}
		}()

		c2, err := client.NewClient(testRPCAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := c2.Close(); err != nil {
				t.Error(err)
			}
		}()
		if err := c2.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := c2.Deactivate(context.Background()); err != nil {
				t.Error(err)
			}
		}()

		f(t, r, c1, c2)
	})
}
