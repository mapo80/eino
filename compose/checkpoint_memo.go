package compose

import (
	"container/list"
	"context"
	"crypto/sha256"
	"sync"

	"github.com/cloudwego/eino/internal/serialization"
)

// checkpointMemoBudget bounds the checkpoints kept in memory, measured in
// the bytes written to the store.
const checkpointMemoBudget = 4 << 20

// checkpointMemos keeps, for the checkpoints most recently written with the
// default serializer, the parsed form of the bytes handed to the store and
// their SHA-256. A read whose bytes have that digest decodes the parsed form
// instead of parsing the bytes again; any other bytes are decoded as usual.
//
// The parsed form shares no memory with the checkpoint that was written and
// is never modified, and every read decodes a new checkpoint from it: the
// writer may keep changing its values, and each reader owns what it gets,
// exactly as when the bytes are decoded.
var checkpointMemos = newCheckpointMemo(checkpointMemoBudget)

// CheckPointOwner is implemented by a CheckPointStore whose checkpoints
// belong to an owner that can go away (a database of a runtime that
// closes): the memo keeps that store's checkpoints under the owner, and
// ForgetCheckPointOwner drops them. The owner is asked with the context of
// each read and write, so a store that picks the actual store from the
// context (one compiled graph serving many runs) names the owner of that
// call. The owner must be comparable. A store without it, or a nil owner,
// shares the memo under no owner, as before.
type CheckPointOwner interface {
	CheckPointOwner(ctx context.Context) any
}

// ForgetCheckPointOwner drops what the memo keeps of the checkpoints
// written through the stores of owner. Without it they stay until other
// checkpoints evict them.
func ForgetCheckPointOwner(owner any) {
	if owner == nil {
		return
	}
	checkpointMemos.forgetOwner(owner)
}

// checkpointMemoKey is a checkpoint id within its store's owner.
type checkpointMemoKey struct {
	owner any
	id    string
}

func checkpointOwnerOf(ctx context.Context, store CheckPointStore) any {
	if o, ok := store.(CheckPointOwner); ok {
		return o.CheckPointOwner(ctx)
	}
	return nil
}

type checkpointMemo struct {
	mu      sync.Mutex
	budget  int
	size    int
	order   *list.List // of *checkpointMemoEntry, most recently used first
	entries map[checkpointMemoKey]*list.Element
}

type checkpointMemoEntry struct {
	id     checkpointMemoKey
	sum    [sha256.Size]byte
	size   int
	parsed *serialization.Parsed
}

func newCheckpointMemo(budget int) *checkpointMemo {
	return &checkpointMemo{budget: budget, order: list.New(), entries: map[checkpointMemoKey]*list.Element{}}
}

// remember records the parsed form of the bytes with the given digest just
// written for id, replacing what was kept for id.
func (m *checkpointMemo) remember(id checkpointMemoKey, sum [sha256.Size]byte, size int, parsed *serialization.Parsed) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(id)
	if size > m.budget {
		return
	}
	m.entries[id] = m.order.PushFront(&checkpointMemoEntry{id: id, sum: sum, size: size, parsed: parsed})
	m.size += size
	for m.size > m.budget {
		m.removeLocked(m.order.Back().Value.(*checkpointMemoEntry).id)
	}
}

// forget drops what was kept for id.
func (m *checkpointMemo) forget(id checkpointMemoKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(id)
}

// forgetOwner drops what was kept for every checkpoint of owner.
func (m *checkpointMemo) forgetOwner(owner any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.entries {
		if id.owner == owner {
			m.removeLocked(id)
		}
	}
}

// lookup returns the parsed form kept for id when data are the bytes it was
// made from.
func (m *checkpointMemo) lookup(id checkpointMemoKey, data []byte) *serialization.Parsed {
	m.mu.Lock()
	el, ok := m.entries[id]
	var entry *checkpointMemoEntry
	if ok {
		entry = el.Value.(*checkpointMemoEntry)
	}
	m.mu.Unlock()
	if entry == nil || entry.size != len(data) || sha256.Sum256(data) != entry.sum {
		return nil
	}
	m.mu.Lock()
	if el, ok := m.entries[id]; ok && el.Value == entry {
		m.order.MoveToFront(el)
	}
	m.mu.Unlock()
	return entry.parsed
}

func (m *checkpointMemo) removeLocked(id checkpointMemoKey) {
	el, ok := m.entries[id]
	if !ok {
		return
	}
	m.size -= el.Value.(*checkpointMemoEntry).size
	m.order.Remove(el)
	delete(m.entries, id)
}
