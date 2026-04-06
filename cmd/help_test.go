package cmd

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRequireExactArgsShowsHelp(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "import <secret-note>",
		Short: "Import a note",
		Long:  "Import a note from another machine.",
		Args:  requireExactArgs(1),
	}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	err := cmd.Args(cmd, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	if !strings.Contains(output.String(), "Import a note from another machine.") {
		t.Fatalf("expected help output, got %q", output.String())
	}
}

func TestDepositArgsShowHelpWhenAmountMissing(t *testing.T) {
	var output bytes.Buffer
	depositCmd.SetOut(&output)
	depositCmd.SetErr(&output)
	t.Cleanup(func() {
		depositCmd.SetOut(nil)
		depositCmd.SetErr(nil)
	})

	if err := depositCmd.Flags().Set("all", "false"); err != nil {
		t.Fatalf("setting --all=false: %v", err)
	}

	err := depositCmd.Args(depositCmd, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", output.String())
	}
}
