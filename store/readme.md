# Key-value Store

This package provides a wrapper for a simple key-value store. The underlying database may be changed later.

Tested key-value packages:
* Pebble: Has many dependencies and increases the binary file size by ~6 MB.
* Pogreb: Currently used. Limited to 4 billion records due to 32-bit uint used as index.
