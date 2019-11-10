package client_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hackerwins/yorkie/client"
	"github.com/hackerwins/yorkie/pkg/document"
	"github.com/hackerwins/yorkie/pkg/document/proxy"
	"github.com/hackerwins/yorkie/testhelper"
	"github.com/hackerwins/yorkie/yorkie"
)

const (
	testRPCAddr    = "localhost:1101"
	testCollection = "test-col"
)

func TestClient(t *testing.T) {
	testhelper.WithYorkie(t, func(t *testing.T, r *yorkie.Yorkie) {
		t.Run("new/close test", func(t *testing.T) {
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
			assert.True(t, cli.IsActive())

			// Already activated
			if err := cli.Activate(ctx); err != nil {
				t.Error(err)
			}
			assert.True(t, cli.IsActive())

			if err := cli.Deactivate(ctx); err != nil {
				t.Error(err)
			}
			assert.False(t, cli.IsActive())

			// Already deactivated
			if err := cli.Deactivate(ctx); err != nil {
				t.Error(err)
			}
			assert.False(t, cli.IsActive())
		})
	})
}

func TestClientAndDocument(t *testing.T) {
	withYorkieAndTwoClients(t, func(t *testing.T, r *yorkie.Yorkie, c1 *client.Client, c2 *client.Client) {
		t.Run("attach/detach test", func(t *testing.T) {
			ctx := context.Background()
			doc := document.New(testCollection, t.Name())
			if err := doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("k1", "k1")
				return nil
			}, "update k1 with k1"); err != nil {
				t.Error(err)
			}

			if err := c1.AttachDocument(ctx, doc); err != nil {
				t.Error(err)
			}
			assert.True(t, doc.IsAttached())

			if err := c1.DetachDocument(ctx, doc); err != nil {
				t.Error(err)
			}
			assert.False(t, doc.IsAttached())
		})

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
			}, "set v1 by c1"); err != nil {
				t.Error(err)
			}

			if err := doc2.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("k1", "v2")
				return nil
			}, "set v1 by c2"); err != nil {
				t.Error(err)
			}
			assert.NotEqual(t, doc1.Marshal(), doc2.Marshal())

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
				root.GetArray("k1").AddString("v2").AddString("v4")
				return nil
			}, "add v2, vj by c1"); err != nil {
				t.Error(err)
			}

			if err := doc2.Update(func(root *proxy.ObjectProxy) error {
				root.GetArray("k1").AddString("v3").AddString("v5")
				return nil
			}, "add v3, v5 by c2"); err != nil {
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

		t.Run("nested array test", func(t *testing.T) {
			ctx1 := context.Background()
			doc1 := document.New(testCollection, t.Name())
			if err := c1.AttachDocument(ctx1, doc1); err != nil {
				t.Error(err)
			}

			if err := doc1.Update(func(root *proxy.ObjectProxy) error {
				root.SetNewArray("k1").
					AddString("v1").
					AddNewArray().AddString("1").AddString("2").AddString("3")
				return nil
			}, "nested update by c1"); err != nil {
				t.Error(err)
			}

			ctx2 := context.Background()
			doc2 := document.New(testCollection, t.Name())
			if err := c2.AttachDocument(ctx2, doc2); err != nil {
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

		t.Run("object set and remove test", func(t *testing.T) {
			ctx1 := context.Background()
			ctx2 := context.Background()

			doc1 := document.New(testCollection, t.Name())
			if err := c1.AttachDocument(ctx1, doc1); err != nil {
				t.Error(err)
			}
			doc2 := document.New(testCollection, t.Name())
			if err := c2.AttachDocument(ctx2, doc2); err != nil {
				t.Error(err)
			}

			if err := doc1.Update(func(root *proxy.ObjectProxy) error {
				root.SetNewObject("k1").
					SetString("k1.1", "v1").
					SetString("k1.2", "v2").
					SetString("k1.3", "v3")
				root.SetNewObject("k2").
					SetString("k2.1", "v4").
					SetString("k2.2", "v5").
					SetString("k2.3", "v6")
				return nil
			}, "nested update by c1"); err != nil {
				t.Error(err)
			}

			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}
			if err := c2.PushPull(ctx2); err != nil {
				t.Error(err)
			}
			assert.Equal(t, doc1.Marshal(), doc2.Marshal())

			if err := doc1.Update(func(root *proxy.ObjectProxy) error {
				root.Remove("k1")
				root.GetObject("k2").Remove("k2.2")
				return nil
			}, "nested update by c1"); err != nil {
				t.Error(err)
			}

			if err := c1.PushPull(ctx1); err != nil {
				t.Error(err)
			}
			if err := c2.PushPull(ctx2); err != nil {
				t.Error(err)
			}
			assert.Equal(t, doc1.Marshal(), doc2.Marshal())
		})
	})
}

func withYorkieAndTwoClients(
	t *testing.T,
	f func(t *testing.T, r *yorkie.Yorkie, c1 *client.Client, c2 *client.Client),
) {
	testhelper.WithYorkie(t, func(t *testing.T, r *yorkie.Yorkie) {
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
