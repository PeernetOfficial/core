// Small code for manual tests/development.

package upnp

import (
	"fmt"
	"net"
	"testing"
)

func TestUPnP(t *testing.T) {
	localIP := net.ParseIP("0.0.0.0")

	nat, err := Discover(localIP)
	if err != nil {
		fmt.Printf("%v\n", err)
		return
	}

	addr, err := nat.GetExternalAddress()
	if err != nil {
		fmt.Printf("%v\n", err)
		return
	}

	fmt.Printf("%s\n", addr.String())
}
