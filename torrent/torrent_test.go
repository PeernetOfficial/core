package torrent

import (
	"fmt"
	"testing"
)

func TestSplit(t *testing.T) {
	// Splitting Test file with each of 15 kb
	err := Split("TestFiles/april.zip", 0.015)
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
}
