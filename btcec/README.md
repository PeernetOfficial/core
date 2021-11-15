# btcec

This is a fork of `https://github.com/btcsuite/btcd/tree/master/btcec`. Last sync of changes: 14.11.2021

Package btcec implements elliptic curve cryptography needed for working with
Bitcoin (secp256k1 only for now). It is designed so that it may be used with the
standard crypto/ecdsa packages provided with go.  A comprehensive suite of test
is provided to ensure proper functionality.  Package btcec was originally based
on work from ThePiachu which is licensed under the same terms as Go, but it has
signficantly diverged since then.  The btcsuite developers original is licensed
under the liberal ISC license.

Although this package was primarily written for btcd, it has intentionally been
designed so it can be used as a standalone package for any projects needing to
use secp256k1 elliptic curve cryptography.

## Examples

* Sign Message: Demonstrates signing a message with a secp256k1 private key that is first parsed form raw bytes and serializing the generated signature.

* Verify Signature: Demonstrates verifying a secp256k1 signature against a public key that is first parsed from raw bytes.  The signature is also parsed from raw bytes.

* Encryption: Demonstrates encrypting a message for a public key that is first parsed from raw bytes, then decrypting it using the corresponding private key.

* Decryption: Demonstrates decrypting a message using a private key that is first parsed from raw bytes.
