# PeerNet S/Kademlia Implementation

This sub-package allows clients on PeerNet to organize into a Kademlia network.
Nodes on the network interact by exchanging signed and encrypted packets, which
themselves convey routing information among peers, as well as requests for
resources (like files) and metadata about those resources.

## DHT vs File Transfer

These files do not provide the mechanisms for transferring heavy files between
peers directly. Peers are instead organized into a coherent and highly resiliant
P2P network which *facilitates the exchange of* direct connection details for
use in file-transfers.
