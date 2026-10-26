package main

import (
	"log"
	"os"

	gofastboot "proto-distro/tools/go_fastboot"
)

func main() {
	if err := gofastboot.RunCLI(os.Args); err != nil {
		log.Fatal(err)
	}
}
