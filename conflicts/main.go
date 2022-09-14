// Put under dir GITALY/cmd/conflicts/main.go
package main

import (
	"bytes"
	"encoding/gob"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime/pprof"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
)

var (
	repoPath string
	ours     string
	theirs   string
)

func main() {
	flag.StringVar(&repoPath, "repo-path", "", "repository path")
	flag.StringVar(&ours, "ours", "", "ours commit")
	flag.StringVar(&theirs, "theirs", "", "their commit")
	flag.Parse()

	f, _ := os.OpenFile("cpu.pprof", os.O_CREATE|os.O_RDWR, 0644)
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	c := git2go.ConflictsCommand{
		Repository: repoPath,
		Ours:       ours,
		Theirs:     theirs,
	}
	fmt.Printf("conflicts command: %+v\n", c)
	input := &bytes.Buffer{}
	output := &bytes.Buffer{}
	e := gob.NewEncoder(input)
	d := gob.NewDecoder(output)

	if err := e.Encode(c); err != nil {
		log.Fatal("encode err: ", err)
	}

	cmd := exec.Command("gitaly-git2go-v15", "conflicts")
	cmd.Stdin = input
	cmd.Stdout = output
	cmd.Stderr = os.Stderr
	fmt.Printf("run command: %v\n", cmd.String())
	err := cmd.Run()
	if err != nil {
		log.Fatalf("run err: %v\n%v\n", err, output.String())
	}

	var result git2go.ConflictsResult
	err = d.Decode(&result)
	if err != nil {
		log.Fatalf("decode err: %v\n%v\n", err, output.String())
	}

	for _, conflict := range result.Conflicts {
		fmt.Printf("ancestor: %v\nours: %v\ntheirs: %v\ncontents: %s\n",
			conflict.Ancestor, conflict.Our, conflict.Their, string(conflict.Content))
	}
}
