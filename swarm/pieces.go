// Package swarm /*
package swarm

/*
As the number of pieces increase, more hash codes need to be stored in the metainfo file.
Therefore, as a rule of thumb, pieces should be selected so that the metainfo file is no larger than 50 – 75kb.
The main reason for this is to limit the amount of hosting storage and bandwidth needed by indexing servers.
The most common piece sizes are 256kb, 512kb and 1mb. The number of pieces is therefore: total length / piece size.
Pieces may overlap file boundaries.
 */

type pieces struct {
	piecesRequired  *fileChunks
	piecesAvailable *fileChunks
}

// 0.1 = 100 KB
// 1.0 = 1 MB
// 1000.0 = 1 GB
var (
   pieceSizeMapping map[float64]float64
)

// initialize Piece size mapping

// GeneratePieces Generates pieces based on the file provided
//func GeneratePieces(fileName string) (*pieces, error ){
//
//}





