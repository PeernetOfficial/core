# UDT: UDP-based Data Transfer Protocol

UDT (UDP-based Data Transfer Protocol) is a transfer protocol on top of UDP. See https://udt.sourceforge.io/ for the original spec and the reference implementation.

This code is a fork from https://github.com/odysseus654/go-udt which itself is a fork.

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

From `udtSocket.Read`:

```
// for datagram sockets, block until we have a message to return and then return it
// if the buffer isn't big enough, return a truncated message (discarding the rest) and return an error

// for streaming sockets, block until we have at least something to return, then
// fill up the passed buffer as far as we can without blocking again
```

According to `DataPacket.SetMessageData`, datagram messages do not set the order flag (bit 29).

## Deviations

MTU negotiation is disabled. Peernet uses a hardcoded max packet size (see protocol package). Packets may be routed through any network adapter, therefore pinning a MTU specific to a network adapter would not make much sense.

The "rendezvous" functionality has been removed since Peernet supports native Traverse messages for UDP hole punching.

Multiplexing multiple UDT sockets to a single UDT connection is removed. It added complexity without benefits in this case. Peernet uses a single UDP port and UDP connection between two peers. Multiplexing has no effect other than breaking the concept and the security of Peernet message sequences.

The order flag (bit 29) set for datagram messages is ignored for security reasons; the behavior whether incoming packets must be ordered or not is hardcoded to whether it is in streaming or datagram mode. 
