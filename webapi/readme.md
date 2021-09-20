# Web API

The web API provides access to core functions via HTTP.

It can be used by local client software to connect to the Peernet network and use functions such as share, search, and download files.

## Use Considerations (when not to use it)

Do not expose this API to the internet. The web API uses the core library which uses the user's public and private key to connect to the network.
It shall only run on a loopback IP such as `127.0.0.1` or `::1`. Special HTTP headers (including the Access-Control headers) are intentionally not set.

The API is unauthenticated and provides direct access to the users blockchain.

## Deployment

The API must be initialized and started before use.

```go
webapi.Start([]string{"127.0.0.1:112"}, false, "", "", 10*time.Second, 10*time.Second)

// To register an additional API endpoint:
webapi.Router.HandleFunc("/newfunction", newFunction).Methods("GET")
```

# Available Functions

These are the functions provided by the API:

```
/status                     Provides current connectivity status to the network
/peer/self                  Provides information about the self peer details

/blockchain/self/header     Header of the blockchain
/blockchain/self/append     Append a block to the blockchain
/blockchain/self/read       Read a block of the blockchain
/blockchain/self/add/file   Add file to the blockchain
/blockchain/self/list/file  List all files stored on the blockchain

/profile/list               List all users profile fields and blobs
/profile/read               Reads a specific users profile field or blob
/profile/write              Writes a specific users profile field or blob
```

# API Documentation

## Informational Functions

### Status

This function informs about the current connection status of the client to the network. Additional fields will be added in the future.

```
Request:    GET /status
Response:   200 with JSON structure apiResponseStatus
```

```go
type apiResponseStatus struct {
	Status        int  `json:"status"`        // Status code: 0 = Ok.
	IsConnected   bool `json:"isconnected"`   // Whether connected to Peernet.
	CountPeerList int  `json:"countpeerlist"` // Count of peers in the peer list. Note that this contains peers that are considered inactive, but have not yet been removed from the list.
	CountNetwork  int  `json:"countnetwork"`  // Count of total peers in the network.
	// This is usually a higher number than CountPeerList, which just represents the current number of connected peers.
	// The CountNetwork number is going to be queried from root peers which may or may not have a limited view into the network.
}
```

### Self Information

This function returns information about the current peer.

```
Request:    GET /peer/self
Response:   200 with JSON structure apiResponsePeerSelf
```

The peer and node IDs are encoded as hex encoded strings.

```go
type apiResponsePeerSelf struct {
	PeerID string `json:"peerid"` // Peer ID. This is derived from the public in compressed form.
	NodeID string `json:"nodeid"` // Node ID. This is the blake3 hash of the peer ID and used in the DHT.
}
```

## Blockchain Functions

### Blockchain Self Header

This function returns information about the current peer. It is not required that a peer has a blockchain. If no data is shared, there are no blocks. The blockchain does not formally have a header as each block has the same structure.

```
Request:    GET /blockchain/self/header
Response:   200 with JSON structure apiBlockchainHeader
```

```go
type apiBlockchainHeader struct {
	PeerID  string `json:"peerid"`  // Peer ID hex encoded.
	Version uint64 `json:"version"` // Current version number of the blockchain.
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks). If 0, no data exists.
}
```

### Blockchain Append Block

This appends a block to the blockchain. This is a low-level function for already encoded blocks.
Do not use this function. Adding invalid data to the blockchain may corrupt it which subsequently might result in blacklisting by other peers.

```
Request:    POST /blockchain/self/append with JSON structure apiBlockchainBlockRaw
Response:   200 with JSON structure apiBlockchainBlockStatus
```

```go
type apiBlockRecordRaw struct {
	Type uint8  `json:"type"` // Record Type. See core.RecordTypeX.
	Data []byte `json:"data"` // Data according to the type.
}

type apiBlockchainBlockRaw struct {
	Records []apiBlockRecordRaw `json:"records"` // Block records in encoded raw format.
}

type apiBlockchainBlockStatus struct {
	Status  int    `json:"status"`  // Status: 0 = Success, 1 = Error invalid data
	Height  uint64 `json:"height"`  // Height of the blockchain (number of blocks).
	Version uint64 `json:"version"` // Version of the blockchain.
}
```

### Blockchain Read Block

This reads a block of the current peer.

```
Request:    GET /blockchain/self/read?block=[number]
Response:   200 with JSON structure apiBlockchainBlock
```

```go
type apiBlockchainBlock struct {
	Status            int                 `json:"status"`            // Status: 0 = Success, 1 = Error block not found, 2 = Error block encoding (indicates that the blockchain is corrupt)
	PeerID            string              `json:"peerid"`            // Peer ID hex encoded.
	LastBlockHash     []byte              `json:"lastblockhash"`     // Hash of the last block. Blake3.
	BlockchainVersion uint64              `json:"blockchainversion"` // Blockchain version
	Number            uint64              `json:"blocknumber"`       // Block number
	RecordsRaw        []apiBlockRecordRaw `json:"recordsraw"`        // Records raw. Successfully decoded records are parsed into the below fields.
	RecordsDecoded    []interface{}       `json:"recordsdecoded"`    // Records decoded. The encoding for each record depends on its type.
}
```

The array `RecordsDecoded` will contain any present record of the following:
* Profile records, see `apiBlockRecordProfile`
* File records, see `apiBlockRecordFile`

```go
type apiBlockRecordProfile struct {
	Fields []apiBlockRecordProfileField `json:"fields"` // All fields
	Blobs  []apiBlockRecordProfileBlob  `json:"blobs"`  // Blobs
}

type apiBlockRecordProfileField struct {
	Type uint16 `json:"type"` // See ProfileFieldX constants.
	Text string `json:"text"` // The data
}

type apiBlockRecordProfileBlob struct {
	Type uint16 `json:"type"` // See ProfileBlobX constants.
	Data []byte `json:"data"` // The data
}
```

```go
type apiBlockRecordFile struct {
	ID          uuid.UUID         `json:"id"`          // Unique ID.
	Hash        []byte            `json:"hash"`        // Blake3 hash of the file data
	Type        uint8             `json:"type"`        // File Type. For example audio or document. See TypeX.
	Format      uint16            `json:"format"`      // File Format. This is more granular, for example PDF or Word file. See FormatX.
	Size        uint64            `json:"size"`        // Size of the file
	Folder      string            `json:"folder"`      // Folder, optional
	Name        string            `json:"name"`        // Name of the file
	Description string            `json:"description"` // Description. This is expected to be multiline and contain hashtags!
	Date        time.Time         `json:"date"`        // Date of the virtual file
	Metadata    []apiFileMetadata `json:"metadata"`    // Metadata. These are decoded tags.
	TagsRaw     []apiFileTagRaw   `json:"tagsraw"`     // All tags encoded that were not recognized as metadata.

	// The following known tags from the core library are decoded into metadata or other fields in above structure; everything else is a raw tag:
	// TagTypeName, TagTypeFolder, TagTypeDescription, TagTypeDateCreated
	// The caller can specify its own metadata fields and fill the TagsRaw structure when creating a new file. It will be returned when reading the files' data.
}

type apiFileMetadata struct {
	Type  uint16 `json:"type"`  // See core.TagTypeX constants.
	Name  string `json:"name"`  // User friendly name of the tag. Use the Type fields to identify the metadata as this name may change.
	Value string `json:"value"` // Text value of the tag.
}

type apiFileTagRaw struct {
	Type uint16 `json:"type"` // See core.TagTypeX constants.
	Data []byte `json:"data"` // Data
}
```

## File Functions

These functions allow adding, deleting, and listing files stored on the users blockchain. Only metadata is actually stored on the blockchain.

### Add File

This adds a file with the provided information to the blockchain. The date field cannot be set by the caller and is ignored.

Any file added is publicly accessible. The user should be informed about this fact in advance. The user is responsible and liable for any files shared.

```
Request:    POST /blockchain/self/add/file with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
```

```go
type apiBlockAddFiles struct {
	Files []apiBlockRecordFile `json:"files"`
}
```

Example POST request to `http://127.0.0.1:112/blockchain/self/add/file`:

```json
{
    "files": [{
        "id": "236de31d-f402-4389-bdd1-56463abdc309",
        "hash": "aFad3zRACbk44dsOw5sVGxYmz+Rqh8ORDcGJNqIz+Ss=",
        "type": 1,
        "format": 10,
        "size": 4,
        "name": "Test.txt",
        "folder": "sample directory/sub folder",
        "description": "",
        "metadata": [],
        "tagsraw": []
    }]
}
```

Another payload example to create a new file but with a new arbitrary tag with type number 100 set to "test" and setting the metadata field "Date Created" (which is type 2 = `core.TagTypeDateCreated`):

```json
{
    "files": [{
        "id": "bc32cbae-011d-4f0b-80a8-281ca93692e7",
        "hash": "aFad3zRACbk44dsOw5sVGxYmz+Rqh8ORDcGJNqIz+Ss=",
        "type": 1,
        "format": 10,
        "size": 4,
        "name": "Test.txt",
        "folder": "sample directory/sub folder",
        "description": "Example description\nThis can be any text #newfile #2021.",
        "metadata": [{
            "type": 2,
            "value": "2021-08-28 00:00:00"
        }],
        "tagsraw":  [{
            "type": 100,
            "data": "dGVzdA=="
        }]
    }]
}
```

### List Files

This lists all files stored on the blockchain.

```
Request:    GET /blockchain/self/list/file
Response:   200 with JSON structure apiBlockAddFiles
```

Example response:

```json
{
    "files": [{
        "id": "a59b6465-fe8c-4a61-9fcc-fe37cf711fd4",
        "hash": "aFad3zRACbk44dsOw5sVGxYmz+Rqh8ORDcGJNqIz+Ss=",
        "type": 1,
        "format": 10,
        "size": 4,
        "folder": "sample directory/sub folder",
        "name": "Test.txt",
        "description": "",
        "date": "2021-08-27T16:59:13+02:00",
        "metadata": [],
        "tagsraw": []
    }],
    "status": 0
}
```

### Delete File

This deletes files from the blockchain with the provided IDs. The blockchain will be refactored, which means it is recalculated without the specified files. The blockchains version number might be increased.

```
Request:    POST /blockchain/self/delete/file with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
```

Example POST request to `http://127.0.0.1:112/blockchain/self/delete/file`:

```json
{
    "files": [{
        "id": "236de31d-f402-4389-bdd1-56463abdc309"
    }]
}
```

Example response:

```json
{
    "status": 0,
    "height": 7,
    "version": 1
}
```

## Profile Functions

### Profile List

```
Request:    GET /profile/list
Response:   200 with JSON structure apiProfileData
```

```go
type apiProfileData struct {
	Fields []apiBlockRecordProfileField `json:"fields"` // All fields
	Blobs  []apiBlockRecordProfileBlob  `json:"blobs"`  // All blobs
	Status int                          `json:"status"` // Status of the operation, only used when this structure is returned from the API. See core.BlockchainStatusX.
}

const (
	BlockchainStatusOK                 = 0 // No problems in the blockchain detected.
	BlockchainStatusBlockNotFound      = 1 // Missing block in the blockchain.
	BlockchainStatusCorruptBlock       = 2 // Error block encoding
	BlockchainStatusCorruptBlockRecord = 3 // Error block record encoding
	BlockchainStatusDataNotFound       = 4 // Requested data not available in the blockchain
)
```

Example request: `http://127.0.0.1:112/profile/list`

Example response:

```json
{
    "fields": [{
        "type": 0,
        "text": "Test Username 2022"
    }, {
        "type": 1,
        "text": "test@example.com"
    }],
    "blobs": null,
    "status": 0
}
```

### Profile Read

This reads a specific users' profile field or blob. For the index see the `ProfileFieldX` and `ProfileBlobX` constants.

```
Request:    GET /profile/read?field=[index] or &blob=[index]
Response:   200 with JSON structure apiProfileData
```

```go
// ProfileFieldX constants define well known profile information
const (
	ProfileFieldName    = 0 // Arbitrary username
	ProfileFieldEmail   = 1 // Email address
	ProfileFieldWebsite = 2 // Website address
	ProfileFieldTwitter = 3 // Twitter account without the @
	ProfileFieldYouTube = 4 // YouTube channel URL
	ProfileFieldAddress = 5 // Physical address
)

// ProfileBlobX constants define well known blobs
// Pictures should be in JPEG or PNG format.
const (
	ProfileBlobPicture = 0 // Profile picture, unspecified size
)
```

Example request to read the users username: `http://127.0.0.1:112/profile/read?field=0`

Example response:

```json
{
    "fields": [{
        "type": 0,
        "text": "Test Username 2022"
    }],
    "blobs": null,
    "status": 0
}
```

### Profile Write

```
Request:    POST /profile/write with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
```

Example POST request to `http://127.0.0.1:112/profile/write`:

```json
{
    "fields": [{
        "type": 0,
        "text": "Test Username 2021"
    }]
}
```

Example response:

```json
{
    "status": 0,
    "height": 1,
    "version": 0
}
```

### Profile Delete

```
Request:    POST /profile/delete with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
```

Example POST request to `http://127.0.0.1:112/profile/delete`:

```json
{
    "fields": [{
        "type": 0
    }]
}
```
