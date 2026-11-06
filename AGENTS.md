# AGENTS.md

Rules for AI agents working in this repository.

## What this repo is

`github.com/PeernetOfficial/core` — the Peernet core library in Go. It is the reference implementation of the Peernet protocol: connectivity, Kademlia DHT, per-user blockchain, warehouse (file storage), UDT transfers, encryption, search. Every Peernet client (first of all the reference client `Cmd`) builds on it. Public on GitHub; `origin` is the source of truth.

## Hard rules

1. **Diff first, commit on approval.** Show the owner the full `git diff` of every change and wait for an explicit approval before committing. No exceptions, including one-line fixes and edits he asked for in chat.
2. **One logical change per commit.** Message = what + why. Push immediately after committing.
3. **Master is live.** Every commit on master builds, passes `go vet` and `go test`, and has been exercised. Nothing half-done, no stubs, no `TODO` that papers over a problem, no silenced errors. Report what real execution returned, never what it should return.
4. **Public repo.** No credentials, hostnames, server names, infrastructure details or internal roadmap. The root peer seed list in `Config Default.yaml` is protocol data (bootstrap), not infrastructure detail.
5. **No third-party contributions.** PRs are not accepted and not merged; the history stays single-author.
6. **Simplicity wins.** If a change grows a feature nobody asked for, cut it. Prefer the simplest design that fully meets the spec.
7. **Underspecified or wrong is reported, not worked around.** Say so plainly, document it, solve it with the owner.

## Layout

| Path | Purpose |
|---|---|
| `*.go` (root, package `core`) | Backend: init, config, network listen/detect, Kademlia, connections, ping, message sending, block/UDT transfers, bootstrap. Entry point is `Init` in `Peernet.go`. |
| `protocol/` | Wire format: packet and message encoding, commands, hashes, sequences. |
| `dht/` | Kademlia routing table, node search client, information requests. |
| `blockchain/` | Per-user blockchain: blocks, records (files, profile, tags). |
| `warehouse/` | Local storage of shared file data, merkle hashing. |
| `merkle/` | Merkle tree and fragments. |
| `search/` | Local search index, term normalization, text-to-hash. |
| `store/` | Key-value store backends (memory, pogreb, pebble). |
| `udt/` | UDT transport implementation (own copy; `udt/packet/` has the packet types). |
| `btcec/` | secp256k1 (own copy). |
| `upnp/`, `reuseport/`, `sanitize/`, `system/` | UPnP port mapping, socket reuse, filename sanitizing, package file reading. |
| `webapi/` | HTTP API on top of the backend; also exports high-level functions clients call directly. |
| `Config Default.yaml` | Default config template written on first run; localhost paths only. |
| `Config.go` | Config loading and the `Version` constant. |
| `README.md` | Public documentation; uses the whitepaper's vocabulary (`client`, `peer`, `root peer`). |

## Code conventions

- Go file names are capitalized words separated by spaces (`Network Detection.go`, `Transfer UDT.go`); match that for new files. Tests are `*_test.go` next to the code.
- Every Go file starts with the header block:
  ```
  /*
  File Name:  <name>.go
  Copyright:  <year> Kleissner Ventures Ltd
  Author:     Peter Kleissner
  */
  ```
  Old `Peernet s.r.o.` headers are updated when the file is touched for real work, not in a bulk sweep.
- Line endings: commit files as they are. Never convert an existing file's line endings, never add a `.gitattributes`; a line-ending-only change is noise in the diff and is rejected. `gofmt -l` flags CRLF files — if `gofmt -l` also lists the HEAD version of the file, it is only the line endings, not a formatting error.
- Exported identifiers get doc comments. Constants that tune protocol behaviour (timeouts, MTU, bucket size, alpha) are named and documented, and the README section that describes them is kept in sync.
- Errors are returned or logged, never swallowed. No panics in library code on network input.
- New dependencies need the owner's approval; keep the module small.

## Build & verify

```bash
go build ./...
go vet ./...
go test ./...
```

All three must be clean before a diff is shown. A change to networking, DHT, transfer or search code is additionally exercised end-to-end through the reference client `Cmd` (github.com/PeernetOfficial/Cmd; `go build` there with a `replace github.com/PeernetOfficial/core => ../core` in a local, uncommitted go.mod edit) against a second running peer, and the report states what the run actually did. Config and data files from test runs stay out of the repo (`.gitignore` covers `*.exe`, `log.txt`, `*debug_bin`, `test.*`).

## Git workflow

- Remote: `https://github.com/PeernetOfficial/core.git`, branch `master`. Wrap git network calls as `GIT_TERMINAL_PROMPT=0 timeout 60 git …`.
- `git pull --ff-only` at session start and again right before every commit; `git status --short` before editing — an unexpectedly dirty tree is reported, never stashed over.
- Version bumps (`Version` in `Config.go`, "Current version" in README) are their own commit, on the owner's call.
