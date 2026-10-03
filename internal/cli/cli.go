// Package cli implements the photobag command line.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"photobag/internal/bag"
	"photobag/internal/query"
)

// Version is set at build time with -ldflags "-X photobag/internal/cli.Version=...".
var Version = "dev"

type globals struct {
	journal     string
	noMigBackup bool
	verbose     bool
}

// Execute runs the CLI.
func Execute() int {
	g := &globals{}
	root := &cobra.Command{
		Use:   "photobag",
		Short: "An organising, deduplicating bag for images, stored in one SQLite file",
		Long: `PhotoBag keeps a photo library — original bytes, thumbnails, thumbprints,
tags and comparison scores — in a single SQLite file (the "bag").

Point any command at a bag file; it is created if it does not exist.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelInfo
			if g.verbose {
				level = slog.LevelDebug
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		},
	}
	root.PersistentFlags().StringVar(&g.journal, "journal", bag.JournalAuto, "journal mode: auto, wal or delete (delete is used automatically on network drives)")
	root.PersistentFlags().BoolVar(&g.noMigBackup, "no-migration-backup", false, "skip the safety copy made before upgrading an older bag")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "verbose logging")

	root.AddCommand(
		serveCmd(g), importCmd(g), exportCmd(g), backupCmd(g),
		dedupCmd(g), analyzeCmd(g), taggerCmd(), filesCmd(g), infoCmd(g), compactCmd(g), refreshCmd(g),
	)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func (g *globals) open(path string) (*bag.Bag, error) {
	b, err := bag.Open(path, bag.Options{Journal: g.journal, NoMigrationBackup: g.noMigBackup})
	if err != nil {
		return nil, err
	}
	if b.Created() {
		fmt.Fprintf(os.Stderr, "Created new bag %s\n", b.Path)
	}
	return b, nil
}

// signalContext is cancelled on Ctrl+C / SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// queryFlags adds image selection flags to a command.
type queryFlags struct {
	tags, anyTags, notTags []string
	glob                   string
	untagged               bool
}

func (q *queryFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&q.tags, "tag", nil, "only images with this tag (repeatable: all must match)")
	cmd.Flags().StringArrayVar(&q.anyTags, "any-tag", nil, "only images with at least one of these tags (repeatable)")
	cmd.Flags().StringArrayVar(&q.notTags, "not-tag", nil, "exclude images with this tag (repeatable)")
	cmd.Flags().StringVar(&q.glob, "glob", "", "only images whose name matches this case-insensitive glob, e.g. \"IMG_*.jpg\"")
	cmd.Flags().BoolVar(&q.untagged, "untagged", false, "only images without any tag")
}

func (q *queryFlags) query() query.ImageQuery {
	return query.ImageQuery{TagsAll: q.tags, TagsAny: q.anyTags, TagsNone: q.notTags, NameGlob: q.glob, Untagged: q.untagged}
}

// status prints a single updating line on terminals.
type status struct {
	tty     bool
	last    time.Time
	w       int
	printed string // last line printed without a terminal
}

func newStatus() *status {
	st, err := os.Stderr.Stat()
	return &status{tty: err == nil && st.Mode()&os.ModeCharDevice != 0}
}

func (s *status) update(force bool, format string, args ...any) {
	if !force && time.Since(s.last) < 200*time.Millisecond {
		return
	}
	s.last = time.Now()
	line := fmt.Sprintf(format, args...)
	if !s.tty {
		if force && line != s.printed {
			s.printed = line
			fmt.Fprintln(os.Stderr, line)
		}
		return
	}
	if r := []rune(line); len(r) > 110 {
		line = string(r[:107]) + "..."
	}
	pad := ""
	if n := s.w - len([]rune(line)); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	s.w = len([]rune(line))
	fmt.Fprintf(os.Stderr, "\r%s%s", line, pad)
}

func (s *status) done() {
	if s.tty && s.w > 0 {
		fmt.Fprintln(os.Stderr)
	}
	s.w = 0
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
