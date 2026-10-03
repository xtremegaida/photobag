package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"photobag/internal/bag"
	"photobag/internal/files"
)

func filesCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage ordinary files (notes, documentation) kept in a bag",
		Long: `Besides images, a bag keeps ordinary files in folders: notes, documentation,
anything that should travel with the library. Paths inside the bag use "/"
and match names ignoring case.`,
	}
	cmd.AddCommand(filesLsCmd(g), filesImportCmd(g), filesExportCmd(g))
	return cmd
}

// folderID resolves a folder path inside the bag ("" is the top level).
func folderID(ctx context.Context, b *bag.Bag, p string) (int64, error) {
	n, err := files.Resolve(ctx, b, p)
	if err != nil {
		if errors.Is(err, files.ErrNotFound) {
			return 0, fmt.Errorf("no folder %q in the bag", p)
		}
		return 0, err
	}
	if n == nil {
		return 0, nil
	}
	if !n.Dir {
		return 0, fmt.Errorf("%q is a file, not a folder", p)
	}
	return n.ID, nil
}

func filesLsCmd(g *globals) *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:   "ls <bag> [path]",
		Short: "List a folder of the bag's files",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx := context.Background()
			p := ""
			if len(args) == 2 {
				p = args[1]
			}
			l, err := files.Browse(ctx, b, p)
			if err != nil {
				if errors.Is(err, files.ErrNotFound) {
					return fmt.Errorf("nothing at %q in the bag", p)
				}
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			var list func(prefix string, nodes []files.Node) error
			list = func(prefix string, nodes []files.Node) error {
				for _, n := range nodes {
					name := prefix + n.Name
					if n.Dir {
						fmt.Fprintf(tw, "%s/\t%s\t%d items\n", name, humanBytes(n.Size), n.Items)
						if recursive {
							kids, err := files.List(ctx, b, n.ID)
							if err != nil {
								return err
							}
							if err := list(name+"/", kids); err != nil {
								return err
							}
						}
						continue
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\n", name, humanBytes(n.Size), time.UnixMilli(n.ModifiedAt).Format("2006-01-02 15:04"))
				}
				return nil
			}
			if l.Node != nil && !l.Node.Dir {
				err = list("", []files.Node{*l.Node})
			} else {
				err = list("", l.Children)
			}
			tw.Flush()
			if err != nil {
				return err
			}
			if l.Node == nil && len(l.Children) == 0 {
				fmt.Println("(no files)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "list the contents of folders too")
	return cmd
}

func conflictFlag(replace, skip bool) (string, error) {
	switch {
	case replace && skip:
		return "", errors.New("choose one of the options for existing files")
	case replace:
		return files.ConflictReplace, nil
	case skip:
		return files.ConflictSkip, nil
	}
	return files.ConflictRename, nil
}

func printTransfer(verb string, rep *files.Report) {
	fmt.Printf("%s %d file(s), %s, in %d new folder(s): %d replaced, %d renamed, %d skipped, %d failed\n",
		verb, rep.Added+rep.Replaced+rep.Renamed, humanBytes(rep.Bytes), rep.Folders, rep.Replaced, rep.Renamed, rep.Skipped, rep.Failed)
	for _, p := range rep.Problems {
		fmt.Printf("  failed: %s: %s\n", p.Path, p.Reason)
	}
}

func filesImportCmd(g *globals) *cobra.Command {
	var to string
	var replace, skip bool
	cmd := &cobra.Command{
		Use:   "import <bag> <file-or-folder>",
		Short: "Copy a file, or a folder with everything in it, into the bag",
		Long: `Copy a file or folder from disk into the bag's files. A folder arrives as
itself, with its whole tree; hidden files and system clutter are left out.
Files whose names are already taken are kept apart with " (2)" unless
--replace or --skip-existing is given.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conflict, err := conflictFlag(replace, skip)
			if err != nil {
				return err
			}
			src, err := filepath.Abs(args[1])
			if err != nil {
				return err
			}
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			parent, err := files.MakeDirs(ctx, b, 0, to)
			if err != nil {
				return err
			}
			st := newStatus()
			rep, err := files.Import(ctx, b, src, parent, conflict, func(p files.Progress) {
				if p.Phase == "scanning" {
					st.update(false, "Scanning… %d files", p.Found)
					return
				}
				st.update(false, "Copying %d/%d  %s", p.Done, p.Found, p.Current)
			})
			st.done()
			if rep != nil {
				printTransfer("Imported", rep)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "folder in the bag to copy into (created if needed; default: the top level)")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace files that already exist")
	cmd.Flags().BoolVar(&skip, "skip-existing", false, "leave files that already exist")
	return cmd
}

func filesExportCmd(g *globals) *cobra.Command {
	var from string
	var replace, skip bool
	cmd := &cobra.Command{
		Use:   "export <bag> <folder>",
		Short: "Copy the bag's files (or one folder or file of them) to a folder on disk",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conflict, err := conflictFlag(replace, skip)
			if err != nil {
				return err
			}
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			var ids []int64
			if strings.Trim(from, "/") != "" {
				n, err := files.Resolve(ctx, b, from)
				if err != nil {
					return fmt.Errorf("nothing at %q in the bag", from)
				}
				ids = []int64{n.ID}
			}
			st := newStatus()
			rep, err := files.Export(ctx, b, ids, args[1], conflict, func(p files.Progress) {
				st.update(false, "Copying %d/%d  %s", p.Done, p.Found, p.Current)
			})
			st.done()
			if rep != nil {
				printTransfer("Exported", rep)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "file or folder in the bag to export (default: everything)")
	cmd.Flags().BoolVar(&replace, "overwrite", false, "overwrite files that already exist")
	cmd.Flags().BoolVar(&skip, "skip-existing", false, "leave files that already exist")
	return cmd
}
