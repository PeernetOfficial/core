/*
File Username:  Block Record.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner

Each record inside the block has this basic structure:
Offset  Size   Info
0       1      Record type
1       8      Date created. This remains the same in case of block refactoring.
9       4      Size of data
13      ?      Data (encoding depends on record type)

*/

package blockchain

// RecordTypeX defines the type of the record
const (
	RecordTypeProfile       = 0 // Profile data about the end user.
	RecordTypeTagData       = 1 // Tag data record to be referenced by one or multiple tags. Only valid in the context of the current block.
	RecordTypeFile          = 2 // File
	RecordTypeInvalid1      = 3 // Do not use.
	RecordTypeCertificate   = 4 // Certificate to certify provided information in the blockchain issued by a trusted 3rd party.
	RecordTypeContentRating = 5 // Content rating (positive).
	RecordTypeContentReport = 6 // Content report (negative).
)

// BlockDecoded contains the decoded records from a block
type BlockDecoded struct {
	Block
	RecordsDecoded []interface{} // Decoded records. See BlockRecordX structures.
}

// decodeBlockRecords decodes all raw records in the block and returns a high-level decoded structure
// Use decodeBlockRecordX instead for specific record decoding.
func decodeBlockRecords(block *Block) (decoded *BlockDecoded, err error) {
	decoded = &BlockDecoded{Block: *block}

	files, err := decodeBlockRecordFiles(block.RecordsRaw, block.NodeID)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		decoded.RecordsDecoded = append(decoded.RecordsDecoded, file)
	}

	if profileFields, err := DecodeBlockRecordProfile(block.RecordsRaw); err != nil {
		return nil, err
	} else if len(profileFields) > 0 {
		decoded.RecordsDecoded = append(decoded.RecordsDecoded, profileFields)
	}

	return decoded, nil
}
