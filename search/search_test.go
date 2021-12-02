package search

import (
	"fmt"
	"github.com/PeernetOfficial/core"
	"testing"
)

// Test written to run the workflow to
// test generated indexes
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

// Test written to run remove Index workflow
func TestRemoveIndexes(t *testing.T) {
	err := RemoveIndexes("My Name is Akilan")
	if err != nil {
		t.Error(err)
	}
}

func TestNormalizeWords(t *testing.T) {
	words, err := NormalizeWords("français")
	if err != nil {
		t.Error(err)
	}
	fmt.Println(words)

	words, err = NormalizeWords("testé-lol_What to do-idk")
	if err != nil {
		t.Error(err)
	}

	fmt.Println(words)

}
