# UDT: UDP-based Data Transfer Protocol

UDT (UDP-based Data Transfer Protocol) is a transfer protocol on top of UDP. See https://udt.sourceforge.io/ for the original spec and the reference implementation.

This project is a fork from https://github.com/odysseus654/go-udt which itself is a fork.

## Stream vs Datagram

```
// TypeSTREAM describes a reliable streaming protocol (e.g. TCP)
TypeSTREAM SocketType = 1

// TypeDGRAM describes a partially-reliable messaging protocol
TypeDGRAM SocketType = 2

UDT supports both reliable data streaming and partial reliable 
messaging. The data streaming semantics is similar to that of TCP, 
while the messaging semantics can be regarded as a subset of SCTP 
[RFC4960]. 
```
