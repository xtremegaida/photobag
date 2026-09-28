package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"photobag/internal/analysis"
	"photobag/internal/library"
)

func analyzeCmd(g *globals) *cobra.Command {
	var q queryFlags
	var opts analysis.Options
	var endpoint, model string
	var concurrency int
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "analyze <bag>",
		Aliases: []string{"analyse"},
		Short:   "Describe images with a vision model (captions, text, Danbooru tags, categories)",
		Long: `Send images to a vision-language model behind an OpenAI-compatible API and
store the results in the bag:

  caption   a short description for people who cannot see the image
  ocr       the text found in the image (empty when there is none)
  danbooru  Danbooru-style tags (optionally also added as PhotoBag tags)
  category  a category, or a main and a sub category, added as tags

The endpoint, model, prompts and pipeline options are the bag's analysis
settings (set them in the UI under Analysis); --endpoint, --model and
--concurrency override them for this run only. The API key comes from the
` + analysis.KeyEnv + ` environment variable or the key saved in the UI.

By default only images without a result are sent (--mode missing). Use
--mode changed to also redo results made with another model or prompt, or
--mode all to redo everything. Results edited by hand are always kept.`,
		Example: `  photobag analyze photos.photobag --pipeline caption --pipeline ocr
  photobag analyze photos.photobag --pipeline category --tag Holiday --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Query = q.query()
			if err := opts.Validate(); err != nil {
				return err
			}
			b, err := g.open(args[0])
			if err != nil {
				return err
			}
			defer b.Close()
			ctx, stop := signalContext()
			defer stop()
			set, err := analysis.Load(ctx, b)
			if err != nil {
				return err
			}
			if endpoint != "" {
				set.Endpoint = endpoint
			}
			if model != "" {
				set.Model = model
			}
			if concurrency > 0 {
				set.Concurrency = concurrency
			}
			set = set.Normalized()
			if err := set.Validate(); err != nil {
				return err
			}
			plan, err := analysis.MakePlan(ctx, b, set, opts)
			if err != nil {
				return err
			}
			var parts []string
			for _, p := range opts.Pipelines {
				parts = append(parts, fmt.Sprintf("%s %d", p, plan.ByPipeline[p]))
			}
			fmt.Fprintf(os.Stderr, "%d image(s) match; %d request(s) needed (%s)", plan.Images, plan.Requests, strings.Join(parts, ", "))
			if plan.Edited > 0 {
				fmt.Fprintf(os.Stderr, "; %d result(s) edited by hand are kept", plan.Edited)
			}
			fmt.Fprintln(os.Stderr, ".")
			if dryRun || plan.Requests == 0 {
				return nil
			}
			keys := &analysis.KeyStore{Path: analysis.DefaultKeyStorePath()}
			key, _, err := keys.Key(set.Endpoint)
			if err != nil {
				return err
			}
			client, err := set.Client(key)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Using %s at %s, %d at a time.\n", orDefault(set.Model, "the default model"), set.Endpoint, set.Concurrency)
			st := newStatus()
			start := time.Now()
			rep, err := analysis.Run(ctx, b, set, client, plan, opts, func(p analysis.Progress) {
				eta := ""
				if p.Done > 0 && p.Done < p.Total {
					left := time.Duration(float64(time.Since(start)) / float64(p.Done) * float64(p.Total-p.Done))
					eta = " · " + left.Round(time.Second).String() + " left"
				}
				st.update(p.Done == p.Total, "Analysing %d/%d · %d failed%s · %s", p.Done, p.Total, p.Failed, eta, p.Current)
			})
			st.done()
			if rep != nil {
				for i, f := range rep.Failures {
					if i == 20 {
						fmt.Fprintf(os.Stderr, "  … and %d more\n", len(rep.Failures)-20)
						break
					}
					fmt.Fprintf(os.Stderr, "  failed: #%d %s (%s): %s\n", f.ImageID, f.Name, f.Pipeline, f.Error)
				}
				fmt.Printf("Stored %d result(s), %d failed, %d skipped in %s (%d prompt + %d reply tokens).\n",
					rep.Stored, rep.Failed, rep.Skipped, (time.Duration(rep.Millis) * time.Millisecond).Round(time.Second),
					rep.PromptTokens, rep.CompletionTokens)
			}
			return err
		},
	}
	q.register(cmd)
	cmd.Flags().StringSliceVarP(&opts.Pipelines, "pipeline", "p", nil, "pipelines to run: "+strings.Join(library.Pipelines, ", ")+" (repeatable or comma-separated)")
	cmd.Flags().StringVar(&opts.Mode, "mode", analysis.ModeMissing, "which images to send: missing, changed or all")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "API base URL, e.g. http://127.0.0.1:1234/v1 (overrides the bag's setting)")
	cmd.Flags().StringVar(&model, "model", "", "model name (overrides the bag's setting)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "requests at a time (overrides the bag's setting)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only report how many requests are needed")
	return cmd
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
