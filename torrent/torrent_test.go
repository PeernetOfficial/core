package torrent

import (
	"fmt"
	"testing"
)

func TestSplit(t *testing.T) {
	// Splitting Test file with each of 100 kb
	output, err := Split("TestingFiles/lime.epub", 0.1,"TestingFiles/output/")
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}

	PrettyPrint(output)
}
