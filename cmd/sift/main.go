package main

import (
	"os"
	"time"

	"github.com/tyrantkhan/sift/internal/cli"
)

func main() {
	os.Exit(cli.Main(&cli.Env{
		Args:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
		Now:    time.Now,
	}))
}
