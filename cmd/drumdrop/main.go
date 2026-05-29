package main

import (
	"fmt"
	"os"
)

const usage = `drumdrop — download a Drumeo/Musora lesson or whole course (personal archival)

Usage:
  drumdrop <lessonOrCourseId | musoraUrl> [options]

Options:
  --out <dir>          output directory (default ./downloads)
  --quality <q>        best | 2160 | 1440 | 1080 | 720 | 480   (default best)
  --limit <N>          only the first N lessons
  --whole-course       from a lesson, walk up and grab the entire parent course
  --resources-only     skip video; fetch only PDFs / play-along audio / sheet music
  --dry-run            list what would be downloaded, download nothing
  -h, --help           show this help

Account:
  drumdrop login       log in (prompts, or set MUSORA_EMAIL/MUSORA_PASSWORD)
  drumdrop whoami      show the logged-in account
  drumdrop logout      clear saved session + credentials

Examples:
  drumdrop 409918                 # one lesson
  drumdrop 409875                 # a whole course (all its lessons)
  drumdrop https://app.musora.com/drumeo/lessons/course/409875/409918 --whole-course
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(1)
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Print(usage)
		return
	case "login":
		exit(cmdLogin())
	case "whoami":
		exit(cmdWhoami())
	case "logout":
		exit(cmdLogout())
	default:
		exit(cmdDownload(args))
	}
}

// exit prints err (if any) to stderr and exits non-zero, otherwise returns.
func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "✖ ", err)
		os.Exit(1)
	}
}
