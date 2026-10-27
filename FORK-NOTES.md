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

Licensed under Apache 2.0 like the upstream project (`LICENSE-APACHE`).
