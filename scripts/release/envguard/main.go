// Command envguard fails if a workflow other than release.yml can run a job in the release
// environment, which holds the release secrets.
package main

import (
	"fmt"
	"os"
)

func main() {
	dir := ".github/workflows"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	found, err := checkDir(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, f := range found {
		fmt.Println(f)
	}
	if len(found) > 0 {
		os.Exit(1)
	}
}
