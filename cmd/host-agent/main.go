package main

import (
	"fmt"

	"serial-platform/internal/buildinfo"
)

func main() {
	fmt.Printf("host-agent %s %s %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
}
