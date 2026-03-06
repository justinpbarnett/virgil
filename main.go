package main

import (
	"fmt"
	"os"

	"github.com/justinpbarnett/virgil/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(version.FullVersion())
		return
	}

	fmt.Println("Hello, World!")
}