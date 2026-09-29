package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"photobag/internal/analysis"
	"photobag/internal/tagger"
)

func taggerCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "tagger",
		Short: "Install and run a local WD tagger for Danbooru tags",
		Long: `The Danbooru tags pipeline can use a WD tagger (SmilingWolf's WD v3 ONNX
models) instead of the vision model: it is much faster and knows the real
Danbooru tags, with a confidence for each.

"photobag tagger install" sets one up on this computer: a Python
environment with ONNX Runtime (CUDA on an NVIDIA GPU, otherwise the CPU),
the server script, and optionally the model. PhotoBag then starts it when
needed (Analysis → Pipelines & prompts → Danbooru tags → A WD tagger →
This computer) and stops it after 10 idle minutes.

The installation lives in ` + tagger.DefaultDir() + `
(set ` + tagger.DirEnv + ` or --dir to use another place).`,
	}
	cmd.PersistentFlags().StringVar(&dir, "dir", tagger.DefaultDir(), "installation directory")
	cmd.AddCommand(taggerInstallCmd(&dir), taggerStatusCmd(&dir), taggerServeCmd(&dir), taggerScriptCmd(), taggerUninstallCmd(&dir))
	return cmd
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// ask asks a yes/no question on the terminal.
func ask(question string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", question, hint)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

func taggerInstallCmd(dir *string) *cobra.Command {
	var opts tagger.InstallOptions
	var download, noDownload, noTest bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set up the tagger on this computer (run again to update or repair it)",
		Long: `Set up the WD tagger on this computer:

  1. find Python 3.10 or later (or use --python),
  2. create a virtual environment in the installation directory,
  3. install ONNX Runtime and the server's packages: onnxruntime-gpu with
     CUDA and cuDNN from pip when an NVIDIA GPU is found (about 1.3 GB;
     --system-cuda uses the system's CUDA instead), otherwise onnxruntime
     for the CPU (--device chooses),
  4. with --download-model, download the model from Hugging Face
     (model.onnx and selected_tags.csv; 1.26 GB for the default model),
  5. start it once and tag a test image.

Without --download-model or --no-download the command asks. To download
the model yourself, put model.onnx and selected_tags.csv from the
repository's page into the installation directory.

Other WD v3 models work too, for example SmilingWolf/wd-vit-tagger-v3
(380 MB, faster) or SmilingWolf/wd-swinv2-tagger-v3 (470 MB).`,
		Example: `  photobag tagger install --download-model
  photobag tagger install --device cpu --model SmilingWolf/wd-vit-tagger-v3`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if download && noDownload {
				return errors.New("choose --download-model or --no-download, not both")
			}
			ctx, stop := signalContext()
			defer stop()
			opts.Dir = *dir
			opts.Out = os.Stderr
			fmt.Fprintf(os.Stderr, "Installing the tagger in %s\n", *dir)
			repo := opts.Model
			if repo == "" {
				repo = tagger.DefaultModel
				if in := tagger.Detect(*dir); in.Model != "" {
					repo = in.Model
				}
			}
			opts.Download = download
			if !download && !noDownload {
				if in := tagger.Detect(*dir); in.ModelReady && in.Model == repo {
					opts.Download = true // only checks the files
				} else if isTerminal(os.Stdin) {
					size := ""
					if n, err := tagger.ModelSize(ctx, repo); err == nil && n > 0 {
						size = ", " + humanBytes(n)
					}
					opts.Download = ask(fmt.Sprintf("Download the model (%s%s) after installing?", repo, size), true)
				}
			}
			st := newStatus()
			var began time.Time
			var lastFile string
			opts.Progress = func(file string, done, total int64) {
				if file != lastFile {
					began, lastFile = time.Now(), file
				}
				rate := ""
				if secs := time.Since(began).Seconds(); secs > 1 {
					rate = fmt.Sprintf(" · %s/s", humanBytes(int64(float64(done)/secs)))
				}
				if total > 0 {
					st.update(done == total, "%s: %s of %s (%.0f%%)%s", file, humanBytes(done), humanBytes(total), float64(done)*100/float64(total), rate)
				} else {
					st.update(false, "%s: %s%s", file, humanBytes(done), rate)
				}
			}
			in, err := tagger.Install(ctx, opts)
			st.done()
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "\nThe tagger is installed in %s (Python %s, %s).\n", in.Dir, in.Python, deviceText(in.Device))
			if !in.ModelReady {
				fmt.Fprintf(os.Stderr, "\nThe model is not downloaded yet. Run %s,\n"+
					"or put %s and %s from https://huggingface.co/%s into\n%s\n", tagger.InstallCommand(in.Dir, true), tagger.ModelFile, tagger.TagsFile, in.Model, in.Dir)
				return nil
			}
			if !noTest {
				if err := selfTest(ctx, in); err != nil {
					return err
				}
			}
			fmt.Fprintln(os.Stderr, "\nTo use it: Analysis → Pipelines & prompts → Danbooru tags → A WD tagger → This computer.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Python, "python", "", "Python interpreter to use (default: the newest suitable one found)")
	f.StringVar(&opts.Device, "device", "auto", "auto (CUDA when an NVIDIA GPU is found), cuda or cpu")
	f.BoolVar(&opts.SystemCUDA, "system-cuda", false, "use the CUDA and cuDNN installed on the system instead of installing them with pip")
	f.BoolVar(&opts.Fresh, "fresh", false, "recreate the Python environment")
	f.StringVar(&opts.Model, "model", "", "Hugging Face repository of a WD v3 model (default "+tagger.DefaultModel+")")
	f.BoolVar(&download, "download-model", false, "download the model files")
	f.BoolVar(&noDownload, "no-download", false, "do not download the model (and do not ask)")
	f.BoolVar(&noTest, "no-test", false, "skip the test run")
	return cmd
}

func deviceText(device string) string {
	if device == "cuda" {
		return "NVIDIA GPU"
	}
	return "CPU"
}

// selfTest starts the installed tagger and tags a test image.
func selfTest(ctx context.Context, in tagger.Installation) error {
	fmt.Fprintln(os.Stderr, "\nStarting the tagger to test it (loading the model can take a minute)…")
	l := &tagger.Local{Dir: in.Dir}
	defer l.Close()
	start := time.Now()
	if _, err := l.Health(ctx); err != nil {
		return fmt.Errorf("the test failed: %v\n(the full output is in %s)", err, filepath.Join(in.Dir, "tagger.log"))
	}
	started := time.Since(start).Round(100 * time.Millisecond)
	res := analysis.CheckTagger(ctx, l)
	if !res.OK {
		return fmt.Errorf("the test failed: %s\n(the full output is in %s)", res.Message, filepath.Join(in.Dir, "tagger.log"))
	}
	fmt.Fprintf(os.Stderr, "%s It started in %s, and tagged a test image in %d ms: %s\n", res.Message,
		started, res.Millis, strings.Join(res.Tags, ", "))
	if in.Device == "cuda" && !res.OnGPU {
		fmt.Fprintf(os.Stderr, "\nWarning: the GPU is not used, so tagging is slow. The reason is in %s;\n"+
			"an NVIDIA driver update often helps (the CUDA packages need a recent driver), or install\n"+
			"with --system-cuda if CUDA and cuDNN are installed.\n", filepath.Join(in.Dir, "tagger.log"))
	}
	return nil
}

func taggerStatusCmd(dir *string) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the local tagger installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			in := tagger.Detect(*dir)
			fmt.Printf("Directory:  %s\n", in.Dir)
			if !in.Installed {
				fmt.Println("Installed:  no")
				fmt.Println(in.Problem)
				return nil
			}
			fmt.Printf("Installed:  %s (Python %s, %s)\n", orDefault(in.InstalledAt, "yes"), in.Python, deviceText(in.Device))
			fmt.Printf("Model:      %s", in.Model)
			if in.ModelReady {
				fmt.Printf(" (%s)\n", humanBytes(in.ModelBytes))
			} else {
				fmt.Println(" (not downloaded)")
				fmt.Println(in.Problem)
				return nil
			}
			if check {
				ctx, stop := signalContext()
				defer stop()
				return selfTest(ctx, in)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "start the tagger and tag a test image")
	return cmd
}

func taggerServeCmd(dir *string) *cobra.Command {
	var host string
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the installed tagger in the foreground (to use it from other computers)",
		Long: `Run the installed tagger server until Ctrl+C. Other PhotoBags can use it
with its address (Analysis → Pipelines & prompts → Danbooru tags → A WD
tagger → A tagger server). Use --host 0.0.0.0 to accept connections from the
network; the server has no password, so only do that on a trusted network.`,
		Example: `  photobag tagger serve --host 0.0.0.0 --port 8000`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := tagger.Command(*dir, host, port)
			if err != nil {
				return err
			}
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			ctx, stop := signalContext()
			defer stop()
			if err := c.Start(); err != nil {
				return err
			}
			done := make(chan error, 1)
			go func() { done <- c.Wait() }()
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				// Ctrl+C reaches the server too; give it a moment to stop.
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					c.Process.Kill()
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "address to listen on (0.0.0.0 for all)")
	cmd.Flags().IntVar(&port, "port", 8000, "port to listen on")
	return cmd
}

func taggerScriptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "script <dir>",
		Short: "Write the tagger server script, its requirements and a README, to host it elsewhere",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := tagger.WriteScript(args[0])
			for _, f := range files {
				fmt.Println(f)
			}
			return err
		},
	}
}

func taggerUninstallCmd(dir *string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the local tagger (its Python environment and model)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			in := tagger.Detect(*dir)
			if !in.Installed && !in.ModelReady {
				fmt.Fprintf(os.Stderr, "No tagger is installed in %s.\n", *dir)
				return nil
			}
			if !yes {
				if !isTerminal(os.Stdin) {
					return errors.New("add --yes to remove the tagger")
				}
				if !ask(fmt.Sprintf("Remove the tagger and its model from %s?", *dir), false) {
					return nil
				}
			}
			if err := tagger.Uninstall(*dir); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "The tagger has been removed.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	return cmd
}
