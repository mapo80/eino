package compose

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/schema"
)

type memoryTreeStore struct {
	bytes map[string][]byte
	trees map[string]any
}

func (m *memoryTreeStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	v, ok := m.bytes[id]
	return v, ok, nil
}
func (m *memoryTreeStore) Set(_ context.Context, id string, b []byte) error {
	m.bytes[id] = append([]byte(nil), b...)
	return nil
}
func (m *memoryTreeStore) GetTree(_ context.Context, id string) (any, bool, error) {
	v, ok := m.trees[id]
	return v, ok, nil
}
func (m *memoryTreeStore) SetTree(_ context.Context, id string, tree any) error {
	m.trees[id] = tree
	return nil
}

type bytesOnlyStore struct{ s *memoryTreeStore }

func (b bytesOnlyStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	return b.s.Get(ctx, id)
}
func (b bytesOnlyStore) Set(ctx context.Context, id string, v []byte) error {
	return b.s.Set(ctx, id, v)
}

func treeTestCheckpoint() *checkpoint {
	return &checkpoint{
		Inputs: map[string]any{
			"node": &schema.Message{Role: schema.User, Content: "a", Extra: map[string]any{
				"list": []any{"x", map[string]any{"deep": "y"}},
				"map":  map[string]any{"k": "v"},
			}},
			"nil": nil,
		},
		SkipPreHandler: map[string]bool{"node": true},
		RerunNodes:     []string{"node"},
	}
}

// The tree path must decode exactly what the byte path decodes, and the stored
// tree must share no memory with the writer or with any reader.
func TestCheckPointTreeStoreIsolatedAndEqualToBytes(t *testing.T) {
	ctx := context.Background()
	store := &memoryTreeStore{bytes: map[string][]byte{}, trees: map[string]any{}}
	tree := newCheckPointer(nil, nil, store, nil)
	byteCP := newCheckPointer(nil, nil, bytesOnlyStore{store}, nil)

	written := treeTestCheckpoint()
	require.NoError(t, tree.set(ctx, "id", written))
	require.NoError(t, byteCP.set(ctx, "id", treeTestCheckpoint()))
	require.Len(t, store.trees, 1)

	// the writer mutates after the write
	msg := written.Inputs["node"].(*schema.Message)
	msg.Content = "changed"
	msg.Extra["map"].(map[string]any)["k"] = "changed"
	msg.Extra["list"].([]any)[1].(map[string]any)["deep"] = "changed"
	written.RerunNodes[0] = "changed"
	written.SkipPreHandler["other"] = true

	first, ok, err := tree.get(ctx, "id")
	require.NoError(t, err)
	require.True(t, ok)
	fromBytes, ok, err := byteCP.get(ctx, "id")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fromBytes, first)

	got := first.Inputs["node"].(*schema.Message)
	require.NotSame(t, msg, got)
	require.Equal(t, "a", got.Content)
	require.Equal(t, "v", got.Extra["map"].(map[string]any)["k"])
	require.Equal(t, "y", got.Extra["list"].([]any)[1].(map[string]any)["deep"])
	require.Equal(t, []string{"node"}, first.RerunNodes)
	require.Equal(t, map[string]bool{"node": true}, first.SkipPreHandler)

	// the reader mutates; a second read still sees the written value
	got.Content = "reader"
	got.Extra["map"].(map[string]any)["k"] = "reader"
	got.Extra["list"].([]any)[1].(map[string]any)["deep"] = "reader"
	first.RerunNodes[0] = "reader"
	delete(first.Inputs, "node")

	second, ok, err := tree.get(ctx, "id")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fromBytes, second)
	require.NotSame(t, got, second.Inputs["node"])
}
