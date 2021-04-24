# DHT Lite

This code is a fork from https://github.com/james-lawrence/kademlia and https://github.com/prettymuchbryce/kademlia with modifications for proper abstraction. All networking code was removed from the original one. This package shall only provide DHT fuctionality.

The following functions are not handled here and must be done by the caller, if desired:
* Remove nodes that are deemed inactive via `dht.RemoveNode`.
* Provide a function `ShouldEvict` to determine if a node shall be evicted in favor of another one.
* Refresh buckets via `dht.RefreshBuckets`.
* The actual store data functions (and associated replication/expiration) are not provided, only the functionality to traverse through the network.
