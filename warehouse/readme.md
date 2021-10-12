# Warehouse

This package manages provides a warehouse for files that are shared by the user (i.e., published via the user's blockchain). Since the blockchain only stores the metadata, the actual file data needs to be stored in a separate local database.

Features:
* Automatic deduplication
* Addressing files based on the data hash
* Read/Write/Delete
* Provide the entire file or parts of it at anytime
* Store files as large as supported by the target disk

## Limitations

There is currently no hard or soft limit of used storage. If the underlying target disk does not have enough available storage, adding new files will fail.

## Implementation

This package uses blake3 for hashing.
