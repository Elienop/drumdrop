package main

import (
	"fmt"
	"os"
)

const usage = `drumdrop — download Drumeo/Musora lessons (personal archival)

Usage:
  drumdrop <lessonOrCourseId|musoraURL> [flags]   download a lesson or course
  drumdrop login | whoami | logout                manage the Musora session

Flags: --out DIR  --quality best|2160|1440|1080|720|480  --limit N
       --whole-course  --resources-only  --dry-run
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Print(usage) // real dispatch wired in Task 10
	}
}
