# UPnP

There are 2 reference implementations which both are based on 'Taipei Torrent'. This UPnP code is a fork mostly from the btcd one with some changes forked from tendermint.
* https://github.com/btcsuite/btcd/blob/master/upnp.go
* https://github.com/tendermint/tendermint/tree/master/p2p/upnp

This library supports only IPv4 UPnP currently. The IPv6 UPnP protocol is specified here: http://upnp.org/specs/arch/UPnP-arch-AnnexAIPv6-v1.pdf

## Special Cases

FritzBox:
* Users must manually enable port sharing for the host. Otherwise the router returns XML error code 606.
* If the internal port is already forwarded under a different external port, error code 718 is returned.
* If the internal port is already forwarded under the same external port, it does not return an error.

## Troubleshooting

Using Wireshark to intercept the UPnP request and response can help.

For Windows there is a UPnP tool here http://miniupnp.free.fr/files/. The tool supports listing existing mappings which is an interesting functionality. It would make sense to implement the list function to delete or reuse any existing mappings.
