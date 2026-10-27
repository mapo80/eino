package compose

import (
	"container/list"
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

type checkpointMemo struct {
	mu      sync.Mutex
	budget  int
	size    int
	order   *list.List // of *checkpointMemoEntry, most recently used first
	entries map[string]*list.Element
}

type checkpointMemoEntry struct {
	id     string
	sum    [sha256.Size]byte
	size   int
	parsed *serialization.Parsed
}

func newCheckpointMemo(budget int) *checkpointMemo {
	return &checkpointMemo{budget: budget, order: list.New(), entries: map[string]*list.Element{}}
}

// remember records the parsed form of the bytes with the given digest just
// written for id, replacing what was kept for id.
func (m *checkpointMemo) remember(id string, sum [sha256.Size]byte, size int, parsed *serialization.Parsed) {
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
func (m *checkpointMemo) forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(id)
}

// lookup returns the parsed form kept for id when data are the bytes it was
// made from.
func (m *checkpointMemo) lookup(id string, data []byte) *serialization.Parsed {
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

func (m *checkpointMemo) removeLocked(id string) {
	el, ok := m.entries[id]
	if !ok {
		return
	}
	m.size -= el.Value.(*checkpointMemoEntry).size
	m.order.Remove(el)
	delete(m.entries, id)
}
