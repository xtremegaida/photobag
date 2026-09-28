package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"photobag/internal/backup"
	"photobag/internal/dedup"
	"photobag/internal/exporter"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/server"
)

func serveCmd(g *globals) *cobra.Command {
	var cfg server.Config
	var open bool
	cmd := &cobra.Command{
		Use:   "serve <bag>",
		Short: "Serve the web UI for a bag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			srv := server.New(b, cfg)
			if err := srv.Listen(); err != nil {
				return err
			}
			url := srv.URL()
			fmt.Printf("PhotoBag is serving %s\n\n    %s\n\nPress Ctrl+C to stop.\n", b.Path, url)
			if cfg.AllowRemote {
				fmt.Println("Warning: --allow-remote accepts connections from other machines; anyone with the link can read and write the bag and the server's files.")
			}
			if open {
				if err := openBrowser(url); err != nil {
					fmt.Fprintln(os.Stderr, "could not open a browser:", err)
				}
			}
			ctx, stop := signalContext()
			defer stop()
			err = srv.Serve(ctx)
			fmt.Println("Stopping…")
			return err
		},
	}
	cmd.Flags().StringVar(&cfg.Addr, "addr", server.DefaultAddr, "listen address")
	cmd.Flags().BoolVar(&open, "open", false, "open the UI in the default browser")
	cmd.Flags().BoolVar(&cfg.Dev, "dev", false, "development mode: use the Vite dev server for the UI")
	cmd.Flags().BoolVar(&cfg.AllowRemote, "allow-remote", false, "accept requests for any host name (LAN access)")
	cmd.Flags().BoolVar(&cfg.NoToken, "no-token", false, "do not require the per-launch access token")
	return cmd
}

func importCmd(g *globals) *cobra.Command {
	var opts importer.Options
	var reportPath string
	cmd := &cobra.Command{
		Use:   "import <bag> <folder-or-file>",
		Short: "Import images from a folder (optionally recursively)",
		Long: `Import images into the bag. Files are identified by content, not extension;
supported formats are JPEG, PNG, GIF, WebP, BMP and TIFF.

Bit-identical files share storage, so importing duplicates costs no space;
use the dedup step (UI or "photobag dedup") to remove redundant images.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			st := newStatus()
			rep, err := importer.Run(ctx, b, args[1], opts, func(p importer.Progress) {
				if p.Phase == "scanning" {
					st.update(false, "Scanning… %d files found", p.Found)
					return
				}
				st.update(p.Phase == "done", "Importing %d/%d  added %d  skipped %d  failed %d  %s",
					p.Done, p.Found, p.Added, p.Skipped, p.Failed, p.Current)
			})
			st.done()
			if rep != nil {
				printImportReport(rep)
				if reportPath != "" {
					data, _ := json.MarshalIndent(rep, "", "  ")
					if werr := os.WriteFile(reportPath, data, 0o644); werr != nil {
						fmt.Fprintln(os.Stderr, "writing report:", werr)
					}
				}
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&opts.Recursive, "recursive", "r", false, "include sub-folders")
	cmd.Flags().StringArrayVar(&opts.Tags, "tag", nil, "tag every imported image (repeatable)")
	cmd.Flags().BoolVar(&opts.TagFolders, "tag-folders", false, "tag images with each folder name of their path")
	cmd.Flags().BoolVar(&opts.SkipIdentical, "skip-identical", false, "skip files whose bytes are already in the bag")
	cmd.Flags().BoolVar(&opts.IncludeRemoved, "include-removed", false, "import files even if an identical image was removed from the bag")
	cmd.Flags().IntVar(&opts.Workers, "workers", 0, "parallel workers (default: CPUs-1)")
	cmd.Flags().StringVar(&reportPath, "report", "", "write the full per-file report as JSON")
	return cmd
}

func printImportReport(rep *importer.Report) {
	fmt.Printf("Imported %d image(s) from %s: %d skipped, %d failed (%.1fs)\n",
		rep.Added, rep.Source, rep.Skipped, rep.Failed, float64(rep.Millis)/1000)
	if rep.Cancelled {
		fmt.Println("Import was interrupted; completed files were kept.")
	}
	reasons := map[string]int{}
	var failures []importer.FileReport
	for _, f := range rep.Files {
		if f.Status == "skipped" {
			reasons[f.Reason]++
		} else {
			failures = append(failures, f)
		}
	}
	for r, n := range reasons {
		fmt.Printf("  skipped %d: %s\n", n, r)
	}
	for i, f := range failures {
		if i == 20 {
			fmt.Printf("  … and %d more failures (use --report)\n", len(failures)-20)
			break
		}
		fmt.Printf("  failed: %s: %s\n", f.Path, f.Reason)
	}
}

func exportCmd(g *globals) *cobra.Command {
	var q queryFlags
	var opts exporter.Options
	var overwrite, skip bool
	cmd := &cobra.Command{
		Use:   "export <bag> <folder>",
		Short: "Export original images to a folder, optionally selected by tags or name",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if overwrite && skip {
				return errors.New("--overwrite and --skip-existing are mutually exclusive")
			}
			switch {
			case overwrite:
				opts.Existing = exporter.ExistingOverwrite
			case skip:
				opts.Existing = exporter.ExistingSkip
			}
			opts.Query = q.query()
			if err := opts.Query.Validate(); err != nil {
				return err
			}
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			st := newStatus()
			rep, err := exporter.Run(ctx, b, args[1], opts, func(p exporter.Progress) {
				st.update(false, "Exporting %d/%d  %s", p.Done, p.Total, p.Current)
			})
			st.done()
			if err != nil {
				return err
			}
			fmt.Printf("Exported %d of %d image(s) (%s) to %s: %d skipped, %d failed\n",
				rep.Written, rep.Total, humanBytes(rep.Bytes), rep.Dir, rep.Skipped, rep.Failed)
			for _, f := range rep.Files {
				fmt.Printf("  %s: %s: %s\n", f.Status, f.Name, f.Reason)
			}
			if rep.Manifest != "" {
				fmt.Println("Manifest:", rep.Manifest)
			}
			return nil
		},
	}
	q.register(cmd)
	cmd.Flags().BoolVar(&opts.KeepStructure, "keep-structure", false, "recreate the folders the images were imported from")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "overwrite existing files")
	cmd.Flags().BoolVar(&skip, "skip-existing", false, "skip files that already exist (default: add a numeric suffix)")
	cmd.Flags().BoolVar(&opts.Manifest, "manifest", false, "write "+exporter.ManifestName+" with ids, tags and scores")
	return cmd
}

func backupCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "backup <bag> <output-file>",
		Short: "Write a compacted, standalone copy of the bag (VACUUM INTO)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			out := args[1]
			if st, err := os.Stat(out); err == nil && st.IsDir() {
				base := strings.TrimSuffix(filepath.Base(b.Path), filepath.Ext(b.Path))
				out = filepath.Join(out, base+"-backup.photobag")
			}
			fmt.Fprintln(os.Stderr, "Backing up…")
			res, err := backup.To(ctx, b, out)
			if err != nil {
				return err
			}
			fmt.Printf("Backup written to %s (%s, %.1fs)\n", res.Path, humanBytes(res.Bytes), float64(res.Millis)/1000)
			return nil
		},
	}
}

func compactCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "compact <bag>",
		Short: "Rebuild the bag file to reclaim free space (VACUUM)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			before, after, err := backup.Compact(context.Background(), b)
			if err != nil {
				return err
			}
			fmt.Printf("Compacted %s: %s -> %s\n", b.Path, humanBytes(before), humanBytes(after))
			return nil
		},
	}
}

func refreshCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh <bag>",
		Short: "Regenerate missing or outdated thumbnails and thumbprints",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			st := newStatus()
			rep, err := importer.Refresh(ctx, b, func(done, total int) {
				st.update(done == total, "Refreshing %d/%d", done, total)
			})
			st.done()
			if rep != nil {
				fmt.Printf("Checked %d image file(s): %d updated, %d failed\n", rep.Checked, rep.Updated, rep.Failed)
			}
			return err
		},
	}
}

func infoCmd(g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "info <bag>",
		Short: "Show bag statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx := context.Background()
			st, err := library.GetStats(ctx, b)
			if err != nil {
				return err
			}
			if asJSON {
				data, _ := json.MarshalIndent(st, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			fmt.Printf("Bag:          %s (%s)\n", st.Name, st.Path)
			fmt.Printf("File:         %s (%s free), schema v%d, journal %s\n",
				humanBytes(st.File.SizeBytes), humanBytes(st.File.FreeBytes), st.File.SchemaVersion, st.File.JournalMode)
			fmt.Printf("Images:       %d active, %d in trash, %d purged\n", st.Images, st.Trashed, st.Purged)
			fmt.Printf("Originals:    %d distinct blobs, %s\n", st.Blobs, humanBytes(st.OriginalBytes))
			fmt.Printf("Tags:         %d\n", st.Tags)
			fmt.Printf("Scoring:      %d metric(s), %d run(s), %d comparison(s)\n", st.Metrics, st.Runs, st.Comparisons)
			tags, err := library.ListTags(ctx, b)
			if err == nil && len(tags) > 0 {
				var parts []string
				for _, t := range tags {
					parts = append(parts, fmt.Sprintf("%s (%d)", t.Name, t.Count))
				}
				fmt.Printf("Tag list:     %s\n", strings.Join(parts, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func dedupCmd(g *globals) *cobra.Command {
	var q queryFlags
	var p dedup.Params
	var threshold float64
	var apply bool
	cmd := &cobra.Command{
		Use:   "dedup <bag>",
		Short: "Find (and optionally remove) duplicate images",
		Long: `Find duplicates and print the proposed clusters (a dry run by default).

  --mode exact    groups bit-identical files
  --mode similar  compares each image with its N closest images by thumbprint
                  and groups pairs whose similarity reaches the threshold

With --apply, proposed duplicates are moved to the trash and merged into the
kept image (its tags are combined; comparisons count for the kept image).
Nothing is permanently deleted until the trash is emptied in the UI.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p.Scope = q.query()
			if err := p.Scope.Validate(); err != nil {
				return err
			}
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			st := newStatus()
			scan, err := dedup.NewScan(ctx, b, p, func(done, total int) {
				st.update(false, "Comparing thumbprints %d/%d", done, total)
			})
			st.done()
			if err != nil {
				return err
			}
			if p.Mode == dedup.ModeExact {
				threshold = 1
			}
			clusters := scan.Clusters(threshold)
			var ids []int64
			for _, c := range clusters {
				ids = append(ids, c.Members...)
			}
			images, err := library.GetImages(ctx, b, ids)
			if err != nil {
				return err
			}
			byID := map[int64]library.Image{}
			for _, im := range images {
				byID[im.ID] = im
			}
			marked := 0
			for i, c := range clusters {
				fmt.Printf("Cluster %d (best similarity %.3f)\n", i+1, c.MaxSim)
				prop := map[int64]bool{}
				for _, id := range c.Proposed {
					prop[id] = true
				}
				for k, id := range c.Members {
					im := byID[id]
					action := "keep  "
					if k > 0 {
						action = "      "
						if prop[id] {
							action = "delete"
							marked++
						}
					}
					fmt.Printf("  %s #%-6d %-40s %5dx%-5d %9s  sim %.3f  %s\n", action, id, im.Name, im.Width, im.Height,
						humanBytes(im.Size), c.KeeperSim[k], im.OriginalPath)
				}
			}
			fmt.Printf("%d cluster(s) among %d image(s); %d image(s) proposed for removal.\n", len(clusters), scan.Scanned, marked)
			if !apply {
				if marked > 0 {
					fmt.Println("Dry run: re-run with --apply to move the proposed duplicates to the trash.")
				}
				return nil
			}
			n, err := dedup.Resolve(ctx, b, dedup.Proposals(clusters))
			if err != nil {
				return err
			}
			fmt.Printf("Moved %d image(s) to the trash.\n", n)
			return nil
		},
	}
	q.register(cmd)
	cmd.Flags().StringVar(&p.Mode, "mode", dedup.ModeSimilar, "exact or similar")
	cmd.Flags().IntVar(&p.Neighbors, "neighbors", dedup.DefaultNeighbors, "similar mode: compare each image with its N closest images")
	cmd.Flags().Float64Var(&threshold, "threshold", dedup.DefaultThreshold, "similar mode: minimum similarity (0-1) to count as a duplicate")
	cmd.Flags().BoolVar(&apply, "apply", false, "move the proposed duplicates to the trash")
	return cmd
}
