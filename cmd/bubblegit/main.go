package main

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/f3xp/bubblegit/internal/git"
	"github.com/f3xp/bubblegit/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bubblegit:", err)
		os.Exit(1)
	}
}

func run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := git.Discover(context.Background(), cwd)
	if err != nil {
		return fmt.Errorf("not inside a git repository: %w", err)
	}
	_, err = tea.NewProgram(ui.New(root)).Run()
	return err
}
