# mapo80/eino — performance patches on top of cloudwego/eino v0.9.22

Branch `perf/v0.9.22` = upstream tag `v0.9.22` (`0a5ca53b`) plus independent
commits. Each commit keeps the public behavior and adds its own test.

1. **compose: drop resolved start nodes from `toValidateMap`** — graph construction
   was O(N²) in the number of nodes. Chain of 1000 typed lambdas: 712 ms → 4.3 ms;
   2000: 2.35 s → 14.3 ms (Apple M2 Pro).
2. **compose: compute predecessor sets once per runner** — `dataPredecessors` and
   `controlPredecessors` depend only on the compiled graph and are now shared
   read-only across runs; channels stay per run.
3. **compose: optional `CheckPointTreeStore`** — in-memory checkpoint stores can keep
   the encoded tree and skip the final byte encoding; other stores are unchanged.
4. **compose: copy the state at interrupts without the byte encoding** — `deepCopyState`
   goes through the serializer's encoded tree instead of encoding and parsing the whole
   state as one document. 2 MiB state, right after a GC: 26.9 MB → 4.8 MB allocated
   with sonic v1.15.4 (native), 42.9 MB → 14.9 MB with sonic v1.15.0 under Go 1.27.
5. **build: require sonic v1.15.4** — the native sonic build covers Go 1.27.
6. **internal/serialization: encode and decode leaves with encoding/json** — leaves and map
   keys are small documents; sonic's decoder allocates a pooled parser with 2 MiB of
   fixed buffers whenever a garbage collection has emptied the pool. Copying a small
   state right after a GC: 2.25 MB → 10.7 KB allocated.
7. **internal/serialization: keep the checkpoint bytes of map keys and invalid UTF-8** — string
   map keys are written with the bytes sonic writes and input with invalid UTF-8 is still
   decoded by sonic; a checkpoint written before 6 reads back the same (`testdata`).

8. **compose: explicit checkpoint codecs for native immutable state** —
   `RegisterCheckpointCodec` separates the existing durable wire from an
   in-memory clone. Immutable state shares its value during tree copies and
   converts only when checkpoint bytes are written or loaded. Concrete nested
   containers and typed nils restore the requested native types. Other values
   retain the existing serializer behavior; the codec adds no wire marker.
   Covered by named codec/container/nil tests, the serialization suite, and
   `TestCheckPointTreeStoreIsolatedAndEqualToBytes`.
9. **compose: read a checkpoint just written without parsing its bytes** — with the
   default serializer, the checkpointer keeps the parsed form of the bytes it hands to
   the store and their SHA-256, for the checkpoints most recently written (at most
   4 MiB of bytes). A read whose bytes have that digest decodes a new checkpoint from
   the parsed form; any other bytes are decoded as before. The parsed form shares no
   memory with the writer and is never modified, so the writer, canceled tasks still
   running and every reader keep owning their values. Checkpoint of 300 messages read
   back: 2.7 MB → 0.78 MB and 27.2k → 14.8k allocations per read.

## Branch `arc/json-v2`, tag `v0.9.22-arc.1` — one JSON library

Base: `perf/checkpoint-memo` at `b1e354e5` (`v0.9.22` + changes 1–9 above), the
version ARC pinned as `v0.10.0-alpha.29.0.20261027190430-b1e354e52d39`. Goal: ARC's
server build links only the standard library's `encoding/json/v2` and
`encoding/json/jsontext`. `go 1.27`.

10. **perf: read and write JSON with encoding/json/v2 instead of sonic** (`7b09cd34`) —
    `internal/einojson`: `encoding/json/v2` with `encoding/json`'s semantics
    (`DefaultOptionsV1`) and sonic's escaping (no HTML or U+2028/2029 escapes), map keys
    sorted. Every sonic call goes through it (tool arguments and results, agent tools,
    plan/execute, adk middlewares, the message parser's key path with `ValueAt`, the
    checkpoint serializer). Checkpoint map keys are quoted by `jsontext.AppendQuote`;
    leaves are read by `encoding/json` only, so change 7 is withdrawn: invalid UTF-8 in a
    checkpoint reads as U+FFFD. `bytedance/sonic` remains a test dependency only.
11. **refactor(schema): remove the Jinja2 format type and gonja** (`790891af`) — `FormatType`
    keeps `FString` and `GoTemplate`; `nikolalohinski/gonja`, `json-iterator/go` and
    `modern-go/reflect2` leave the module. The multiquery default prompt is a GoTemplate
    with the same text.
12. **perf(serialization): write and read the checkpoint wire as jsontext tokens**
    (`7688d8a7`) — `internalStruct`/`valueType` implement `MarshalJSONTo`/`UnmarshalJSONFrom`
    (no reflection; names and type keys through a 256-entry string cache);
    `einojson.Marshal` writes into a pooled buffer.

Equivalence (tests in the fork):
- `internal/einojson`: same bytes as sonic's default configuration (keys sorted) for
  structs, tags, omitempty, maps, nil/empty slices, floats at the edges, int64/uint64
  limits, `RawMessage`, `[]byte`, `time.Time`, `,string`; same values and error outcomes on
  27 inputs into struct, map, interface and raw targets (case variants, duplicates, exact
  and out-of-range numbers, nulls, escapes, trailing data, malformed input); `ValueAt` =
  `sonic.GetFromString` bytes on 8 documents × 15 paths. Tested differences: invalid
  UTF-8 written and read as U+FFFD (sonic copied the bytes); `\b` `\f` instead of
  `\u0008` `\u000c`; a number beyond float64 kept as `RawMessage` is accepted.
- `internal/serialization`: map keys = `einojson.MarshalString`, equal to sonic's bytes
  for valid UTF-8 without `\b`/`\f` and read back identical in every case; leaves decode
  as sonic on valid UTF-8; the token codec reads and writes as reflection
  (`TestWire*`).
- `compose`: `TestCheckpointWrittenWithSonicReadsBack` — the checkpoint written with
  sonic (`testdata/checkpoint_33e49389.json`) reads back equal to the one written now;
  the encodings have the same content (key order and the spelling of invalid UTF-8,
  `\b`, `\f` in keys aside).
- Upstream suite green (`go test ./...`), as on the base.

Measures (`BenchmarkCheckpointJSON`, a 20-turn conversation with tool calls, Apple M2 Pro,
go1.27.1, n=8, machine shared with other loads, times indicative):

| | sonic (base) | json/v2 + tokens |
|---|---|---|
| marshal | 332 µs, 211 KiB, 2 328 allocs | 246 µs, 176 KiB, 2 204 allocs |
| unmarshal | 372 µs, 317 KiB, 3 477 allocs | 574 µs (±11 %), 182 KiB, 3 338 allocs |

Licensed under Apache 2.0 like the upstream project (`LICENSE-APACHE`).
