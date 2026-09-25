package main

import (
	"errors"
	"fmt"
	"io"
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
  drumdrop follow @<slug>         follow an instructor (or --instructor <slug>); a
                                  name ("@Jared Falk", quoted) or a coach-page link
                                  works too. Flags: --brand (default drumeo, or
                                  the link's), --quality (default best)
  drumdrop unfollow <followId>    stop following (id from 'drumdrop follows')
  drumdrop follows                list everything you follow
  drumdrop sync [options]         one-shot: download every not-yet-downloaded
                                  lesson under each follow. Flags: --out (default
                                  $DRUMDROP_DOWNLOADS_DIR or ./downloads),
                                  --quality, --limit N (cap new downloads),
                                  --dry-run, --resources-only
  drumdrop daemon [options]       unattended auto-sync: periodically check every
                                  follow for new lessons and download them, one at
                                  a time, with retry. sync is the one-shot
                                  equivalent. Flags: --interval (default 12h),
                                  --once (one cycle then exit), --out, --quality,
                                  --resources-only
  drumdrop serve [options]        run the HTTP API + auto-sync daemon together
                                  until Ctrl-C. Flags: --listen (default
                                  $DRUMDROP_LISTEN or 127.0.0.1:8080), --interval
                                  (default 12h), --out, --quality,
                                  --resources-only. A non-loopback bind requires
                                  $DRUMDROP_API_TOKEN.

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
  drumdrop daemon                 # run unattended, auto-syncing every 12h
  drumdrop daemon --once          # one plan+drain cycle then exit (cron-friendly)
`

// main is the only line no test runs: os.Exit would end the test binary.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches args (the command line without the program name) to its
// command and returns the exit code: 0 for --help or a command that succeeded,
// 1 for no arguments or no download target (after printing the usage) or a
// command that failed (after printing its error). The commands write their own output to
// os.Stdout; stdout and stderr receive only the usage and the error line.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 1
	}
	var err error
	switch args[0] {
	case "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "login":
		err = cmdLogin()
	case "whoami":
		err = cmdWhoami()
	case "logout":
		err = cmdLogout()
	case "follow":
		err = cmdFollow(args[1:])
	case "unfollow":
		err = cmdUnfollow(args[1:])
	case "follows":
		err = cmdFollows()
	case "sync":
		err = cmdSync(args[1:])
	case "daemon":
		err = cmdDaemon(args[1:])
	case "serve":
		err = cmdServe(args[1:])
	default:
		err = cmdDownload(args)
	}
	if errors.Is(err, errNoTarget) {
		fmt.Fprint(stdout, usage)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "✖ ", err)
		return 1
	}
	return 0
}
