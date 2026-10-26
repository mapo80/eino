# mapo80/eino — performance patches on top of cloudwego/eino v0.9.22

Branch `perf/v0.9.22` = upstream tag `v0.9.22` (`0a5ca53b`) plus three independent
commits. Each commit keeps the public behavior and adds its own test.

1. **compose: drop resolved start nodes from `toValidateMap`** — graph construction
   was O(N²) in the number of nodes. Chain of 1000 typed lambdas: 712 ms → 4.3 ms;
   2000: 2.35 s → 14.3 ms (Apple M2 Pro).
2. **compose: compute predecessor sets once per runner** — `dataPredecessors` and
   `controlPredecessors` depend only on the compiled graph and are now shared
   read-only across runs; channels stay per run.
3. **compose: optional `CheckPointTreeStore`** — in-memory checkpoint stores can keep
   the encoded tree and skip the final byte encoding; other stores are unchanged.

Licensed under Apache 2.0 like the upstream project (`LICENSE-APACHE`).
