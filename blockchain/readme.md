# Blockchain

The blockchain stores the metadata of files published by the user, profile data, and social interactions. The blockchain is implemented according to the Peernet Whitepaper published at [peernet.org](https://peernet.org).

The blockchain is a consecutive sequence of blocks linked together by their previous hash. Each block may contain one or multiple records.

All blocks and the blockchain header are stored locally in a key-value database.

# Encoding

## Header

The blockchain header is not part of the Peernet specification. Below is the encoding of the blockchain header. The public key can be extracted from the signature.

```
Offset  Size   Info
0       8      Height of the blockchain
8       8      Version of the blockchain
16      2      Format of the blockchain. This provides backward compatibility.
18      65     Signature
```

## Block

Encoding of a block (it is the same stored in the database and shared in a message):

```
Offset  Size   Info
0       65     Signature of entire block
65      32     Hash (blake3) of last block. 0 for first one.
97      8      Blockchain version number
105     4      Block number
109     4      Size of entire block including this header
113     2      Count of records that follow
```

Each record inside the block has this basic structure:

```
Offset  Size   Info
0       1      Record type
1       8      Date created. This remains the same in case of block refactoring.
9       4      Size of data
13      ?      Data (encoding depends on record type)
```

# Internals

## Block Size

The block size is currently recommended to be slightly below 64 KB (minus message header overhead), so that it fits within a single UDP packet. Having a block size smaller than the max. message size reduces complexity when exchanging individual blocks and increases performance for operations such as file search.

## Edge Cases

### Deleting vs Replacing Records

If a specific record shall be replaced, it should be deleted and a new block containing the replacement record shall be created.

Inline replacement of a record in a block would lead to problems:
* The block size could increase which could push the block size above the recommended limit.
* In case of `RecordTypeFile` records, they may use `RecordTypeTagData` records for compression. If a single record is to be replaced 1:1 with another record, this could not take advantage of this embedded compression algorithm.
