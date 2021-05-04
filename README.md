# Peernet Core

The core library which is needed for any Peernet application. It provides connectivity to the network and all basic functions. For details about Peernet see https://peernet.org/. For the current technical roadmap and upcoming releases see the [Talk forum](https://talk.peernet.org/discussion/10/technical-roadmap).

Current version: 0.2 (pre alpha 2)

Current development status: Initial connectivity works. DHT functionality is in development.

## Use

```go
package main

import (
    "fmt"
    "os"

    "github.com/PeernetOfficial/core"
)

func init() {
    if status, err := core.LoadConfig("Config.yaml"); err != nil {
        fmt.Printf("Error loading config file: %s", err.Error())
        os.Exit(1)
    }

    core.InitLog()
    core.Init()
    core.UserAgent = "Your application/1.0"
}

func main() {
    core.Connect()

    // use functions from core package, for example to find and download files
}
```

## Encryption and Hashing functions

* Salsa20 is used for encrypting the packets.
* secp256k1 is used to generate the peer IDs (public keys).
* blake3 is used for hashing the packets when signing and as hashing algorithm for the DHT.

## Dependencies

Go 1.16 or higher is required. These are the major dependencies:

```
github.com/btcsuite/btcd/btcec
lukechampine.com/blake3
```

## Configuration

Peernet follows a "zeroconf" approach, meaning there is no manual configuration required. However, in certain cases such as providing root peers that shall listen on a fixed IP and port, it is desirable to create a config file. See inline documentation in the file [Config Default.yaml](Config%20Default.yaml).

The name of the config file is passed to the function `LoadConfig`. If it does not exist, it will be created with the values from the file `Config Default.yaml`. It uses the YAML format. Any public/private keys in the config are hex encoded. Here are some notable settings:

* `PrivateKey` The users Private Key hex encoded. The users public key is derived from it.
* `Listen` defines IP:Port combinations to listen on. If not specified, it will listen on all IPs. You can specify an IP but port 0 for auto port selection. IPv6 addresses must be in the format "[IPv6]:Port".

Root peer = A peer operated by a known trusted entity. They allow to speed up the network including discovery of peers and data.

### Private Key

The Private Key is required to make any changes to the user's blockchain, including deleting, renaming, and adding files on Peernet, or nuking the blockchain. If the private key is lost, no write access will be possible. Users should always create a secure backup of their private key.

## Connectivity

### Bootstrap Strategy

* Connection to root peers (initial seed list):
  * Immediate contact to all root peers.
  * Phase 1: First 10 minutes. Try every 7 seconds to connect to all root peers until at least 2 peers connected.
  * Phase 2: After that (if not 2 peers), try every 5 minutes to connect to remaining root peers for a maximum of 1 hour.
* Local peer discovery via IPv4 Broadcast and IPv6 Multicast:
  * Send out immediately when starting.
  * Phase 1: Resend every 10 seconds until at least 1 peer in the peer list.
  * Phase 2: Every 10 minutes.
* Network adapter changes:
  * Check every 10 seconds for new/removed network interfaces and for new/removed IPs.
  * Automatically listen on any newly detected network interfaces and IPs.
  * Start a full Kademlia bucket refresh in case of a new network interface or IP.
* Kademlia bootstrap:
  * As soon as there are at least 2 peers, keep refreshing buckets (target number is alpha) every 10 seconds 3 times.

### Ping

The Ping/Pong commands are used to verify whether connections remain valid. They are only sent in absence of any other commands in the defined timeframe.

* Active connections
  * Invalidate if 'Last packet in' was older than 22 seconds ago.
  * Send ping if last ping and 'Last packet in' was earlier than 10 seconds.
  * Redundant connections to the same peer (= any connections exceeding the 1 main active one): Mentioned times are multiplied by 4.
* Inactive connections
  * If inactive for 120 seconds, remove the connection.
  * If there are no connections (neither active/inactive) to the peer, remove it.
  * Send ping if last ping was earlier than 10 seconds.

Above limits are constants and can be adjusted in the code via `pingTime`, `connectionInvalidate`, and `connectionRemove`.

### Kademlia

The routing table has a bucket size of 20 and the size of keys 256 bits (blake3 hash). Nodes within buckets are sorted by least recently seen. The number of nodes to contact concurrently in DHT lookups (also known as alpha number) is set to 5.

If a bucket is full when a new peer connects `ShouldEvict` is called. It compares the RTTs (favoring smaller one) and in absence of the RTT time it will favor the node which is closer by XOR distance. Refresh of buckets is done every 5 minutes and queries a random ID in that bucket if there are not at least alpha nodes. A full refresh of all buckets is done every hour.

### Timeouts

* The default reply timeout (round-trip time) is 20 seconds set in `ReplyTimeout`. This applies to Response and Pong messages. The RTT timeout implies an average minimum connection speed between peers of about 6.4 KB/s for files of 64 KB size.
* Separate timeouts for file transfers will be established.

## Contributing

Please note that by contributing code, documentation, ideas, snippets, or any other intellectual property you agree that you have all the necessary rights and you agree that we, the Peernet organization, may use it for any purpose.

&copy; 2021 Peernet s.r.o.
