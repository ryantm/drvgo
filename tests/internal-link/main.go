package main

import (
	"fmt"
	"net"
	"os/user"
)

func main() {
	ips, err := net.LookupIP("localhost")
	if err != nil || len(ips) == 0 {
		panic("localhost lookup failed")
	}
	u, err := user.Current()
	if err != nil || u.Uid == "" {
		panic("user lookup failed")
	}
	fmt.Println("native lookups work")
}
