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

Follows (incremental, deduped archival):
  drumdrop follow <id|url>        follow a node (course/series/lesson)
  drumdrop follow @<slug>         follow an instructor (or --instructor <slug>)
                                  flags: --brand (default drumeo), --quality (default best)
  drumdrop unfollow <followId>    stop following (id from 'drumdrop follows')
  drumdrop follows                list everything you follow
  drumdrop sync [options]         download every not-yet-downloaded lesson under
                                  each follow. Flags: --out (default ./downloads),
                                  --quality, --limit N (cap new downloads), --dry-run,
                                  --resources-only

Account:
  drumdrop login       log in (prompts, or set MUSORA_EMAIL/MUSORA_PASSWORD)
  drumdrop whoami      show the logged-in account
  drumdrop logout      clear saved session + credentials

Examples:
  drumdrop 409918                 # one lesson
  drumdrop 409875                 # a whole course (all its lessons)
  drumdrop https://app.musora.com/drumeo/lessons/course/409875/409918 --whole-course
  drumdrop follow 409875          # track a course; new lessons sync later
  drumdrop follow @aaron-edgar    # track an instructor's lessons
  drumdrop sync --limit 5         # download up to 5 new lessons across all follows
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
	case "follow":
		exit(cmdFollow(args[1:]))
	case "unfollow":
		exit(cmdUnfollow(args[1:]))
	case "follows":
		exit(cmdFollows())
	case "sync":
		exit(cmdSync(args[1:]))
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
