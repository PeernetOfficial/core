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
/status                         Provides current connectivity status to the network
/peer/self                      Provides information about the self peer details

/blockchain/self/header         Header of the blockchain
/blockchain/self/append         Append a block to the blockchain
/blockchain/self/read           Read a block of the blockchain
/blockchain/self/add/file       Add file to the blockchain
/blockchain/self/list/file      List all files stored on the blockchain
/blockchain/self/delete/file    Delete files from the blockchain

/profile/list                   List all profile fields
/profile/read                   Read a profile field
/profile/write                  Write profile fields
/profile/delete                 Delete profile fields

/search                         Submit a search request
/search/result                  Return search results
/search/terminate               Terminate a search

/download/start                 Start the download of a file
/download/status                Get the status of a download
/download/action                Pause, resume, and cancel a download

/explore                        List recently shared files

/file/format                    Detect file type and format
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

Common status codes returned by various endpoints:

```go
const (
    BlockchainStatusOK                 = 0 // No problems in the blockchain detected.
    BlockchainStatusBlockNotFound      = 1 // Missing block in the blockchain.
    BlockchainStatusCorruptBlock       = 2 // Error block encoding
    BlockchainStatusCorruptBlockRecord = 3 // Error block record encoding
    BlockchainStatusDataNotFound       = 4 // Requested data not available in the blockchain
)
```

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
* File records, see `apiFile`

## File Functions

These functions allow adding, deleting, and listing files stored on the users blockchain. Only metadata is actually stored on the blockchain. To download a remote file both the file hash and the node ID are required. The node ID specifies the owner of the file.

```go
type apiFile struct {
    ID          uuid.UUID         `json:"id"`          // Unique ID.
    Hash        []byte            `json:"hash"`        // Blake3 hash of the file data
    Type        uint8             `json:"type"`        // File Type. For example audio or document. See TypeX.
    Format      uint16            `json:"format"`      // File Format. This is more granular, for example PDF or Word file. See FormatX.
    Size        uint64            `json:"size"`        // Size of the file
    Folder      string            `json:"folder"`      // Folder, optional
    Name        string            `json:"name"`        // Name of the file
    Description string            `json:"description"` // Description. This is expected to be multiline and contain hashtags!
    Date        time.Time         `json:"date"`        // Date shared
    NodeID      []byte            `json:"nodeid"`      // Node ID, owner of the file
    Metadata    []apiFileMetadata `json:"metadata"`    // Additional metadata.
}

type apiFileMetadata struct {
    Type uint16 `json:"type"` // See core.TagX constants.
    Name string `json:"name"` // User friendly name of the metadata type. Use the Type fields to identify the metadata as this name may change.
    // Depending on the exact type, one of the below fields is used for proper encoding:
    Text   string    `json:"text"`   // Text value. UTF-8 encoding.
    Blob   []byte    `json:"blob"`   // Binary data
    Date   time.Time `json:"date"`   // Date
    Number uint64    `json:"number"` // Number
}
```

Below is the list of defined metadata types. Undefined types may be used by clients, but are always mapped into the `blob` field. Virtual tags are generated at runtime and are read-only. They cannot be stored on the blockchain.

| Type | Constant       | Encoding | Virtual | Info                                                                                                                   |
|------|----------------|----------|---------|------------------------------------------------------------------------------------------------------------------------|
| 0    | TagName        | Text     |         | Mapped into Name field. Name of file.                                                                                  |
| 1    | TagFolder      | Text     |         | Mapped into Folder field. Folder name.                                                                                 |
| 2    | TagDescription | Text     |         | Mapped into Description field. Arbitrary description of the file. May contain hashtags.                                |
| 3    | TagDateShared  | Date     | x       | Mapped into Date field. When the file was published on the blockchain.                                                 |
| 4    | TagDateCreated | Date     |         | Date when the file was originally created.                                                                             |
| 5    | TagSharedByCount    | Number   | x       | Count of peers that share the file.                                                                             |
| 6    | TagSharedByGeoIP    | Text/CSV  | x       | GeoIP data of peers that are sharing the file. CSV encoded with header "latitude,longitude".                    |

### Add File

This adds a file with the provided information to the blockchain. The date field cannot be set by the caller and is ignored.

Any file added is publicly accessible. The user should be informed about this fact in advance. The user is responsible and liable for any files shared.

```
Request:    POST /blockchain/self/add/file with JSON structure apiBlockAddFiles
Response:   200 with JSON structure apiBlockchainBlockStatus
```

```go
type apiBlockAddFiles struct {
    Files  []apiFile `json:"files"`  // List of files
    Status int       `json:"status"` // Status of the operation, only used when this structure is returned from the API.
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
        "metadata": []
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
            "date": "2021-08-28T00:00:00Z"
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
        "date": "2021-08-27T14:59:13Z",
        "nodeid": "0Zo9QHCF06Nrbxgg9s4Q4wYpcHzsQhSMsmftQqjanVI=",
        "metadata": []
    }, {
        "id": "bc32cbae-011d-4f0b-80a8-281ca9369211",
        "hash": "aFad3zRACbk44dsOw5sVGxYmz+Rqh8ORDcGJNqIz+Ss=",
        "type": 1,
        "format": 10,
        "size": 4,
        "folder": "sample directory/sub folder",
        "name": "Test 2.txt",
        "description": "Example description\nThis can be any text #newfile #2021.",
        "date": "2021-09-27T23:33:37Z",
        "nodeid": "0Zo9QHCF06Nrbxgg9s4Q4wYpcHzsQhSMsmftQqjanVI=",
        "metadata": [{
            "type": 2,
            "name": "Date Created",
            "text": "",
            "blob": null,
            "date": "2021-08-28T00:00:00Z"
        }]
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

User profile data such as the username, email address, and picture are stored on the blockchain. Profile fields are text (UTF-8) or binary encoded, depending on the type.

Note that all profile data is arbitrary and shall be considered untrusted and unverified. To establish trust, the user must load Certificates into the blockchain that validate certain data.

Below is the list of well known profile information. Clients may define additional fields. The purpose of this defined list is to provide a common mapping across different client software. Undefined types are always mapped into the `blob` field.

| Type | Constant       | Encoding | Info                          |
|------|----------------|----------|-------------------------------|
| 0    | ProfileName    | Text     | Arbitrary username            |
| 1    | ProfileEmail   | Text     | Email address                 |
| 2    | ProfileWebsite | Text     | Website address               |
| 3    | ProfileTwitter | Text     | Twitter account without the @ |
| 4    | ProfileYouTube | Text     | YouTube channel URL           |
| 5    | ProfileAddress | Text     | Physical address              |
| 6    | ProfilePicture | Blob     | Profile picture               |

### Profile List

This lists all profile fields.

```
Request:    GET /profile/list
Response:   200 with JSON structure apiProfileData
```

```go
type apiProfileData struct {
    Fields []apiBlockRecordProfile `json:"fields"` // All fields
    Status int                     `json:"status"` // Status of the operation, only used when this structure is returned from the API. See core.BlockchainStatusX.
}

type apiBlockRecordProfile struct {
    Type uint16 `json:"type"` // See ProfileX constants.
    // Depending on the exact type, one of the below fields is used for proper encoding:
    Text string `json:"text"` // Text value. UTF-8 encoding.
    Blob []byte `json:"blob"` // Binary data
}
```

Example request: `http://127.0.0.1:112/profile/list`

Example response:

```json
{
    "fields": [{
        "type": 0,
        "text": "Test Username 2021",
        "blob": null
    }, {
        "type": 1,
        "text": "test@example.com",
        "blob": null
    }],
    "status": 0
}
```

### Profile Read

This reads a specific profile field. See ProfileX for recognized fields.

```
Request:    GET /profile/read?field=[index]
Response:   200 with JSON structure apiProfileData
```

Example request to read the users username: `http://127.0.0.1:112/profile/read?field=0`

Example response:

```json
{
    "fields": [{
        "type": 0,
        "text": "Test Username 2021",
        "blob": null
    }],
    "status": 0
}
```

### Profile Write

This writes profile fields. It can write multiple fields at once. See ProfileX for recognized fields.

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

This function allows to delete profile fields. Only the type number is required. Multiple fields can be deleted at the same time.

```
Request:    POST /profile/delete with JSON structure apiProfileData
Response:   200 with JSON structure apiBlockchainBlockStatus
```

Example POST request to `http://127.0.0.1:112/profile/delete` (deleting the profile name):

```json
{
    "fields": [{
        "type": 0
    }]
}
```

## Search API

The search API provides a high-level function to search for files in Peernet. Searching is always asynchronous. `/search` returns an UUID which is used to loop over `/search/result` until the search is terminated.

The current implementation of the underlying search algorithm only searches file names.

Filters and sort order may be applied when starting the search at `/search`, or at runtime when returning the results at `/search/result`.

These are the available sort options:

| Sort | Constant       |  Info                          |
|------|----------------|-------------------------------|
| 0    | SortNone    | No sorting. Results are returned as they come in.  |
| 1    | SortRelevanceAsc    | Least relevant results first.  |
| 2    | SortRelevanceDec    | Most relevant results first.  |
| 3    | SortDateAsc    | Oldest first.  |
| 4    | SortDateDesc    | Newest first.  |
| 5    | SortNameAsc    | File name ascending. The folder name is not used for sorting.  |
| 6    | SortNameDesc    | File name descending. The folder name is not used for sorting.  |
| 7    | SortSizeAsc    | File size ascending. Smallest files first. |
| 8    | SortSizeDesc    | File size descending. Largest files first. |
| 9    | SortSharedByCountAsc    | Shared by count ascending. Files that are shared by the least count of peers first. |
| 10   | SortSharedByCountDesc    | Shared by count descending. Files that are shared by the most count of peers first. |

The following filters are supported:

* Filter by date from and to. Both dates are required. The inclusion check for the 'from date' is >= and 'to date' <.
* File type such as binary, text document etc. See core.TypeX.
* File format (which is more granular) such as PDF, Word, Ebook, etc. See core.FormatX.

### Submitting a Search Request

This starts a search request and returns an ID that can be used to collect the results asynchronously. Note that some of the filters described below (such as `filetype`) must be set to -1 if they are not used.

```
Request:    POST /search with JSON SearchRequest
Response:   200 on success with JSON SearchRequestResponse
```

```go
type SearchRequest struct {
    Term        string      `json:"term"`       // Search term.
    Timeout     int         `json:"timeout"`    // Timeout in seconds. 0 means default. This is the entire time the search may take. Found results are still available after this timeout.
    MaxResults  int         `json:"maxresults"` // Total number of max results. 0 means default.
    DateFrom    string      `json:"datefrom"`   // Date from, both from/to are required if set. Format "2006-01-02 15:04:05".
    DateTo      string      `json:"dateto"`     // Date to, both from/to are required if set. Format "2006-01-02 15:04:05".
    Sort        int         `json:"sort"`       // See SortX.
    TerminateID []uuid.UUID `json:"terminate"`  // Optional: Previous search IDs to terminate. This is if the user makes a new search from the same tab. Same as first calling /search/terminate.
    FileType    int         `json:"filetype"`   // File type such as binary, text document etc. See core.TypeX. -1 = not used.
    FileFormat  int         `json:"fileformat"` // File format such as PDF, Word, Ebook, etc. See core.FormatX. -1 = not used.
    SizeMin     int         `json:"sizemin"`    // Min file size in bytes. -1 = not used.
    SizeMax     int         `json:"sizemax"`    // Max file size in bytes. -1 = not used.
}

type SearchRequestResponse struct {
    ID     uuid.UUID `json:"id"`     // ID of the search job. This is used to get the results.
    Status int       `json:"status"` // Status of the search: 0 = Success (ID valid), 1 = Invalid Term, 2 = Error Max Concurrent Searches
}
```

Example POST request to `http://127.0.0.1:112/search`:

```json
{
    "term": "Test Search",
    "timeout": 10,
    "maxresults": 1000,
    "sort": 0,
    "filetype": -1,
    "fileformat": -1,
    "sizemin": -1,
    "sizemax": -1
}
```

Example response:

```json
{
    "id": "ac5efa64-d403-4a57-8259-c7b7dfb09667",
    "status": 0
}
```

### Returning Search Results

This function returns search results. The default limit is 100.

If reset is set, all results will be filtered and sorted according to the provided parameters. This means that the new first result will be returned again and internal result offset is set to 0. Note that most filters must be set to -1 if they are not used (see the field comments in the `SearchRequest` structure in `/search` above).

```
Request:    GET /search/result?id=[UUID]&limit=[max records]
            Optional parameters:
            &reset=[0|1] to reset the filters or sort orders with any of the below parameters (all required):
            &filetype=[File Type]
            &fileformat=[File Format]
            &from=[Date From]&to=[Date To]
            &sizemin=[Minimum file size]
            &sizemax=[Maximum file size]
            &sort=[sort order]
Result:     200 with JSON structure SearchResult. Check the field status.
```

```go
type SearchResult struct {
    Status int       `json:"status"` // Status: 0 = Success with results, 1 = No more results available, 2 = Search ID not found, 3 = No results yet available keep trying
    Files  []apiFile `json:"files"`  // List of files found
}
```

Example request: `http://127.0.0.1:112/search/result?id=ac5efa64-d403-4a57-8259-c7b7dfb09667&limit=10`

Example response with dummy data:

```json
{
    "status": 1,
    "files": [{
        "id": "b5b0706c-817c-492f-8203-5005c59f110c",
        "hash": "Mv6O773ytkJ5jSjLoy2EvHQaM5KfVppJHeTppMc7alA=",
        "type": 1,
        "format": 14,
        "size": 10,
        "folder": "",
        "name": "88d8cc57d5c2a5fea881ceea09503ee4.txt",
        "description": "",
        "date": "2021-09-23T00:00:00Z",
        "nodeid": "j4yHzmCXiXqg4DPhowj0DIOuuyJxQflo2QSNG3yhCK8=",
        "metadata": [{
            "type": 5,
            "name": "Shared By Count",
            "text": "",
            "blob": null,
            "date": "0001-01-01T00:00:00Z",
            "number": 7
        }, {
            "type": 6,
            "name": "Shared By GeoIP",
            "text": "25.7766,-178.1275\n-46.4041,8.0066\n84.4478,8.2417\n14.1721,-9.7539\n-67.2364,127.6007\n-75.1604,106.7583\n70.5132,-133.4146",
            "blob": null,
            "date": "0001-01-01T00:00:00Z",
            "number": 0
        }]
    }]
}
```

### Terminating a Search

The user can terminate a search early using this function. This helps save system resources and should be considered best practice once a search is no longer needed (for example when the user closes the tab or window that shows the results).

```
Request:    GET /search/terminate?id=[UUID]
Response:   204 Empty
```

## Download API

Downloads can have these status types:

| Status | Constant       |  Info                          |
|------|----------------|-------------------------------|
| 0    | DownloadWaitMetadata    | Wait for file metadata.  |
| 1    | DownloadWaitSwarm    | Wait to join swarm.  |
| 2    | DownloadActive    | Active downloading. It could still be stuck at any percentage (including 0%) if no seeders are available.  |
| 3    | DownloadPause    | Paused by the user.  |
| 4    | DownloadCanceled    | Canceled by the user before the download finished. Once canceled, a new download has to be started if the file shall be downloaded.  |
| 5    | DownloadFinished    | Download finished 100%.  |

The API response codes for download functions are:

| Status | Constant       |  Info                          |
|------|----------------|-------------------------------|
| 0    | DownloadResponseSuccess    | Success  |
| 1    | DownloadResponseIDNotFound    | Error: Download ID not found.  |
| 2    | DownloadResponseFileInvalid   | Error: Target file cannot be used. For example, permissions denied to create it.  |
| 3    | DownloadResponseActionInvalid  | Error: Invalid action. Pausing a non-active download, resuming a non-paused download, or canceling already canceled or finished download.  |
| 4    | DownloadResponseFileWrite    | Error writing file.  |

### Start Download

This starts the download of a file. The path is the full path on disk to store the file.
The hash parameter identifies the file to download. The node ID identifies the blockchain (i.e., the "owner" of the file). The hash and node must be hex-encoded.

```
Request:    GET /download/start?path=[target path on disk]&hash=[file hash to download]&node=[node ID]
Result:     200 with JSON structure apiResponseDownloadStatus
```

```go
type apiResponseDownloadStatus struct {
    APIStatus      int       `json:"apistatus"`      // Status of the API call. See DownloadResponseX.
    ID             uuid.UUID `json:"id"`             // Download ID. This can be used to query the latest status and take actions.
    DownloadStatus int       `json:"downloadstatus"` // Status of the download. See DownloadX.
    File           apiFile   `json:"file"`           // File information. Only available for status >= DownloadWaitSwarm.
    Progress       struct {
        TotalSize      uint64  `json:"totalsize"`      // Total size in bytes.
        DownloadedSize uint64  `json:"downloadedsize"` // Count of bytes download so far.
        Percentage     float64 `json:"percentage"`     // Percentage downloaded. Rounded to 2 decimal points. Between 0.00 and 100.00.
    } `json:"progress"` // Progress of the download. Only valid for status >= DownloadWaitSwarm.
    Swarm struct {
        CountPeers uint64 `json:"countpeers"` // Count of peers participating in the swarm.
    } `json:"swarm"` // Information about the swarm. Only valid for status >= DownloadActive.
}
```

Example request: `http://127.0.0.1:112/download/start?path=test.bin&hash=cde13a55f41e387480391c47238acfe9c0136dd56bf365b01416aec03eec7dc4&node=5a0f712822ddc49633d27df6009d3efa27f19cb371319837f04160bdbda38544`

Example response (only apistatus, id, and downloadstatus are used):

```json
{
    "apistatus": 0,
    "id": "a6107122-9e31-42d3-b663-0df64263c6bc",
    "downloadstatus": 0
}
```

### Get Download Status

This returns the status of an active download.

```
Request:    GET /download/status?id=[download ID]
Result:     200 with JSON structure apiResponseDownloadStatus
```

Example request: `http://127.0.0.1:112/download/status?id=a6107122-9e31-42d3-b663-0df64263c6bc`

```json
{
    "apistatus": 0,
    "id": "950316e8-23b4-49c7-83dd-c021e793129e",
    "downloadstatus": 5,
    "file": {
        "id": "78ac46dc-6731-4f3d-a9d4-22c9a4eb5fb9",
        "hash": "LiQUdqPD78+e6j1eS+0VmSUdCgUXVDN74ELVTRcgmWc=",
        "type": 0,
        "format": 13,
        "size": 10240,
        "folder": "",
        "name": "a96dc7b6a4a7a401c48f93c442f01de9.bin",
        "description": "",
        "date": "2021-10-04T04:37:17Z",
        "nodeid": "lMP3/nYMjoE/PfGKRDZi+ms5h7jWUrdIZaKSvLAAq6A=",
        "metadata": []
    },
    "progress": {
        "totalsize": 10240,
        "downloadedsize": 1024,
        "percentage": 10
    },
    "swarm": {
        "countpeers": 0
    }
}
```

### Pause, Resume, and Cancel a Download

This pauses, resumes, and cancels a download. Once canceled, a new download has to be started if the file shall be downloaded.
Only active downloads can be paused. While a download is in discovery phase (querying metadata, joining swarm), it can only be canceled.
Action: 0 = Pause, 1 = Resume, 2 = Cancel.

```
Request:    GET /download/action?id=[download ID]&action=[action]
Result:     200 with JSON structure apiResponseDownloadStatus (using APIStatus and DownloadStatus)
```

## Explore

### List Recently Shared Files

This returns recently shared files in Peernet. Results are returned in real-time. The file type is an optional filter.

```
Request:    GET /explore?limit=[max records]&type=[file type]
Result:     200 with JSON structure SearchResult. Check the field status.
```

Example request to list 20 recently shared files (all file types): `http://127.0.0.1:112/explore&limit=20`

Example request to list 10 recent documents: `http://127.0.0.1:112/explore?type=5&limit=10`

## Helper Functions

These helper functions are usually not needed, but can be useful in special cases.

### Detect file type and file format

This function detects the file type and file format of the specified file. It will primarily use the file extension for detection. If unavailable, it uses the first 512 bytes of the file data to detect the type.

```
Request:    GET /file/format?path=[file path on disk]
Result:     200 with JSON structure apiResponseFileFormat
```

```go
type apiResponseFileFormat struct {
    Status     int    `json:"status"`     // Status: 0 = Success, 1 = Error reading file
    FileType   uint16 `json:"filetype"`   // File Type.
    FileFormat uint16 `json:"fileformat"` // File Format.
}
```

Example request: `http://127.0.0.1:112/file/format?path=test.txt`

Example response:

```json
{
    "status": 0,
    "filetype": 1,
    "fileformat": 10
}
```
