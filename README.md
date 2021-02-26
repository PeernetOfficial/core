# Peernet Core

The core library which is needed for any Peernet application. It provides connectivity to the network and all basic functions.

Current version: 0.1 (pre-alpha)

## Encryption and Hashing functions

* Salsa20 is used for encrypting the packets.
* secp256k1 is used to generate the peer IDs (public keys).
* blake3 is used for hashing the packets when signing.

## Dependencies

Before compiling, make sure to download and update all 3rd party packages:

```
go get -u github.com/btcsuite/btcd/btcec
go get -u github.com/libp2p/go-reuseport
go get -u lukechampine.com/blake3
```

## Configuration

Peernet follows a "zeroconf" approach, meaning there is no manual configuration required. However, in certain cases such as providing root peers [1] that shall listen on a fixed IP and port, it is desirable to create a config file.

The name of the config file is hard-coded to `Settings.yaml`. If it does not exist, it will be created with default values. It uses the YAML format. Any public/private keys in the config are hex encoded. Here are some notable settings:

* `ListenWorkers` defines the count of concurrent workers processing packets (decrypting them and then taking action). Default 2.
* `Listen` defines IP:Port combinations to listen on. If not specified, it will listen on all IPs. You can specify an IP but port 0 for auto port selection. IPv6 addresses must be in the format "[IPv6]:Port".

[1] Root peer = A peer operated by a known trusted entity. They allow to speed up the network including discovery of peers and data. 

## Contributing

Please note that by contributing code, documentation, ideas, snippets, or any other intellectual property you agree that you have all the necessary rights and you agree that we, the Peernet Foundation, may use it for any purpose.

&copy; 2021 Peernet Foundation
