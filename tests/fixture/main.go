package main

import (
	"example.com/libnix-fixture/cgouse"
	"example.com/libnix-fixture/dep"
	"example.com/libnix-fixture/message"
	"fmt"
)

func main() {
	glob, err := message.Glob.ReadFile("assets/message.txt")
	if err != nil || string(glob) != message.Text {
		panic("glob embed mismatch")
	}
	literal, err := message.Files.ReadFile("assets/nested/space $ asset.txt")
	if err != nil || string(literal) != "literal asset\n" {
		panic("embed filename was interpreted by the shell")
	}
	nested, err := message.Files.ReadFile("assets/nested/data.txt")
	if err != nil {
		panic(err)
	}
	hidden, err := message.Hidden.ReadFile("assets/.hidden")
	if err != nil {
		panic(err)
	}
	if cgouse.Answer() != 42 {
		panic("native cross-package link failed")
	}
	fmt.Printf("%s%s%s% x\n%s", message.Text, dep.Value(), nested, message.Bytes, hidden)
}
