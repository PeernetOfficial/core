package webapi

import (
	"fmt"
	"testing"
)

// Test function
func TestDecodeBlake3Hash(t *testing.T) {
	hash, bool := DecodeBlake3Hash("")
	fmt.Println(hash)
	fmt.Println(bool)
}
