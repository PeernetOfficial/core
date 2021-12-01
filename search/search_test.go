package search

import (
	"fmt"
	"github.com/PeernetOfficial/core"
	"testing"
)

func TestGenerateIndexes(t *testing.T) {

	core.SqliteSearchIndexMigration()

	_, err := GenerateIndexes("My Name is Akilan")
	if err != nil {
		t.Error(err)
	}

	search, err := Search("Name")
	if err != nil {
		t.Error(err)
	}

	for i := range search {
		fmt.Println(search[i])
	}

}

func TestRemoveIndexes(t *testing.T) {
	err := RemoveIndexes("My Name is Akilan")
	if err != nil {
		t.Error(err)
	}
}
