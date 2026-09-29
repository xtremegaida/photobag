package tagger

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed python/tagger_server.py
var script []byte

//go:embed python/requirements.txt
var requirements []byte

//go:embed python/README.md
var readme []byte

// Files of an installation.
const (
	ScriptFile = "tagger_server.py"
	ModelFile  = "model.onnx"
	TagsFile   = "selected_tags.csv"
	infoFile   = "install.json"
	logFile    = "tagger.log"
	venvDir    = "venv"
)

// DefaultModel is the Hugging Face repository downloaded by default.
const DefaultModel = "SmilingWolf/wd-eva02-large-tagger-v3"

// DirEnv overrides the installation directory.
const DirEnv = "PHOTOBAG_TAGGER_DIR"

// DefaultDir is where "photobag tagger install" puts the tagger:
// %LOCALAPPDATA%\PhotoBag\tagger on Windows, ~/.local/share/photobag/tagger
// on Linux.
func DefaultDir() string {
	if d := os.Getenv(DirEnv); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "PhotoBag", "tagger")
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "photobag", "tagger")
	}
	if h, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "darwin" {
			return filepath.Join(h, "Library", "Application Support", "PhotoBag", "tagger")
		}
		return filepath.Join(h, ".local", "share", "photobag", "tagger")
	}
	return "photobag-tagger"
}

// WriteScript writes the server script, its requirements and a README to
// dir, for hosting the tagger on another computer.
func WriteScript(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files := map[string][]byte{ScriptFile: script, "requirements.txt": requirements, "README.md": readme}
	var out []string
	for _, name := range []string{ScriptFile, "requirements.txt", "README.md"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, files[name], 0o644); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

// requirementsFor lists the packages for a device: onnxruntime for the
// CPU, onnxruntime-gpu for CUDA (with CUDA and cuDNN from pip unless the
// system's are used).
func requirementsFor(device string, pipCUDA bool) string {
	ort := "onnxruntime>=1.17"
	if device == "cuda" {
		ort = "onnxruntime-gpu>=1.17"
		if pipCUDA {
			ort = "onnxruntime-gpu[cuda,cudnn]>=1.21"
		}
	}
	var b strings.Builder
	for _, line := range strings.Split(string(requirements), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "onnxruntime") {
			line = ort
		}
		if line != "" {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// installInfo is install.json, written by Install.
type installInfo struct {
	Model       string `json:"model"`
	Device      string `json:"device"`
	PipCUDA     bool   `json:"pipCuda"`
	Python      string `json:"python"`
	BasePython  string `json:"basePython"`
	Script      string `json:"script"` // sha256 of the installed script
	InstalledAt string `json:"installedAt"`
}

func readInfo(dir string) (installInfo, error) {
	var in installInfo
	raw, err := os.ReadFile(filepath.Join(dir, infoFile))
	if err != nil {
		return in, err
	}
	return in, json.Unmarshal(raw, &in)
}

func writeInfo(dir string, in installInfo) error {
	js, _ := json.MarshalIndent(in, "", "  ")
	return os.WriteFile(filepath.Join(dir, infoFile), append(js, '\n'), 0o644)
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// VenvPython is the interpreter of the installation's environment.
func VenvPython(dir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(dir, venvDir, "bin", "python")
}

// ModelName is the model's short name, e.g. wd-eva02-large-tagger-v3.
func (in Installation) ModelName() string {
	if in.Model == "" {
		return "WD tagger"
	}
	return in.Model[strings.LastIndex(in.Model, "/")+1:]
}

// Detect inspects the installation in dir.
func Detect(dir string) Installation {
	in := Installation{Dir: dir}
	info, infoErr := readInfo(dir)
	in.Model, in.Device, in.Python, in.InstalledAt = info.Model, info.Device, info.Python, info.InstalledAt
	_, pyErr := os.Stat(VenvPython(dir))
	_, scriptErr := os.Stat(filepath.Join(dir, ScriptFile))
	in.Installed = infoErr == nil && pyErr == nil && scriptErr == nil
	mst, modelErr := os.Stat(filepath.Join(dir, ModelFile))
	_, tagsErr := os.Stat(filepath.Join(dir, TagsFile))
	in.ModelReady = modelErr == nil && tagsErr == nil
	if modelErr == nil {
		in.ModelBytes = mst.Size()
	}
	in.Ready = in.Installed && in.ModelReady
	repo := in.Model
	if repo == "" {
		repo = DefaultModel
	}
	switch {
	case !in.Installed:
		in.Problem = "The tagger is not installed on this computer. Run: " + InstallCommand(dir, false)
	case !in.ModelReady:
		in.Problem = fmt.Sprintf("The model is not downloaded. Run: %s (or put %s and %s from https://huggingface.co/%s in %s)",
			InstallCommand(dir, true), ModelFile, TagsFile, repo, dir)
	}
	return in
}

// InstallCommand is the command that installs the tagger in dir.
func InstallCommand(dir string, download bool) string {
	cmd := "photobag tagger install"
	if download {
		cmd += " --download-model"
	}
	if abs, err := filepath.Abs(dir); err == nil && abs != DefaultDir() {
		cmd += ` --dir "` + abs + `"`
	}
	return cmd
}

// updateScript replaces the installed script with this build's, unless
// someone changed the installed copy.
func updateScript(dir string) error {
	path := filepath.Join(dir, ScriptFile)
	cur, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Equal(cur, script) {
		return nil
	}
	info, err := readInfo(dir)
	if err != nil || info.Script != sha(cur) {
		return nil // edited by hand: keep it
	}
	if err := os.WriteFile(path, script, 0o644); err != nil {
		return err
	}
	info.Script = sha(script)
	return writeInfo(dir, info)
}

// Python is an interpreter found on this computer.
type Python struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	// Conda is set for Anaconda and Miniconda interpreters, which are
	// only used when there is nothing else: on Windows their older C++
	// runtime crashes onnxruntime.
	Conda bool `json:"conda"`
	// Problem is why it cannot be used, if it cannot.
	Problem string `json:"problem,omitempty"`
	major   int
	minor   int
}

func (p Python) String() string {
	if p.Conda {
		return fmt.Sprintf("Python %s from conda (%s)", p.Version, p.Path)
	}
	return fmt.Sprintf("Python %s (%s)", p.Version, p.Path)
}

// Supported Python versions: onnxruntime needs 3.10 or later; newer
// versions than 3.14 may not have its packages yet.
const (
	minMinor = 10
	maxMinor = 14
)

const probeScript = `import os, sys
print("%d.%d.%d" % tuple(sys.version_info[:3]))
print(sys.executable)
try:
    import venv, ensurepip
    print("ok")
except Exception as e:
    print("venv: %s" % e)
print("conda" if os.path.isdir(os.path.join(sys.base_prefix, "conda-meta")) else "")
`

// probePython runs an interpreter to learn its version and whether it
// can create environments.
func probePython(ctx context.Context, argv ...string) (Python, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := append(append([]string{}, argv[1:]...), "-c", probeScript)
	cmd := exec.CommandContext(ctx, argv[0], args...)
	hideWindow(cmd)
	out, err := cmd.Output()
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")), "\n")
	if err != nil || len(lines) < 3 {
		return Python{}, fmt.Errorf("%s did not run", strings.Join(argv, " "))
	}
	p := Python{Version: strings.TrimSpace(lines[0]), Path: strings.TrimSpace(lines[1])}
	p.Conda = len(lines) > 3 && strings.TrimSpace(lines[3]) == "conda"
	parts := strings.Split(p.Version, ".")
	if len(parts) >= 2 {
		p.major, _ = strconv.Atoi(parts[0])
		p.minor, _ = strconv.Atoi(parts[1])
	}
	switch {
	case p.major != 3 || p.minor < minMinor:
		p.Problem = fmt.Sprintf("Python 3.%d or later is needed", minMinor)
	case strings.TrimSpace(lines[2]) != "ok":
		p.Problem = "it cannot create virtual environments (" + strings.TrimPrefix(strings.TrimSpace(lines[2]), "venv: ") + ")"
		if runtime.GOOS == "linux" {
			p.Problem += fmt.Sprintf("; on Debian or Ubuntu install python3.%d-venv", p.minor)
		}
	}
	return p, nil
}

var pyLauncherLine = regexp.MustCompile(`^\s*-(?:V:)?(\d+\.\d+)\S*\s+\*?\s*(.+?)\s*$`)

// FindPythons lists the Python interpreters on this computer, the most
// suitable first.
func FindPythons(ctx context.Context) []Python {
	var cands [][]string
	if runtime.GOOS == "windows" {
		if out, err := hidden(exec.CommandContext(ctx, "py", "-0p")).Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if m := pyLauncherLine.FindStringSubmatch(line); m != nil && strings.HasSuffix(strings.ToLower(m[2]), ".exe") {
					cands = append(cands, []string{m[2]})
				}
			}
		}
		cands = append(cands, []string{"python"}, []string{"python3"})
	} else {
		for m := maxMinor; m >= minMinor; m-- {
			cands = append(cands, []string{fmt.Sprintf("python3.%d", m)})
		}
		cands = append(cands, []string{"python3"}, []string{"python"})
	}
	seen := map[string]bool{}
	var found []Python
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		p, err := probePython(ctx, c...)
		if err != nil {
			continue
		}
		key := p.Path
		if real, err := filepath.EvalSymlinks(key); err == nil {
			key = real // python3 and python are often the same file
		}
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		found = append(found, p)
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if (a.Problem == "") != (b.Problem == "") {
			return a.Problem == ""
		}
		if ai, bi := a.minor <= maxMinor, b.minor <= maxMinor; ai != bi {
			return ai
		}
		if a.Conda != b.Conda {
			return !a.Conda
		}
		return a.minor > b.minor
	})
	return found
}

// GPU is an NVIDIA GPU found with nvidia-smi.
type GPU struct {
	Name   string
	Driver string
	Memory string
}

// DetectGPU looks for an NVIDIA GPU.
func DetectGPU(ctx context.Context) (GPU, bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := hidden(exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,driver_version,memory.total", "--format=csv,noheader")).Output()
	if err != nil {
		return GPU{}, false
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	f := strings.Split(line, ",")
	if len(f) < 3 || strings.TrimSpace(f[0]) == "" {
		return GPU{}, false
	}
	return GPU{Name: strings.TrimSpace(f[0]), Driver: strings.TrimSpace(f[1]), Memory: strings.TrimSpace(f[2])}, true
}

// InstallOptions configure Install.
type InstallOptions struct {
	Dir string
	// Python is the interpreter to use; the best one found when empty.
	Python string
	// Device is auto (CUDA when an NVIDIA GPU is found), cuda or cpu.
	Device string
	// SystemCUDA uses CUDA and cuDNN installed on the system instead of
	// installing them with pip.
	SystemCUDA bool
	// Fresh recreates the Python environment.
	Fresh bool
	// Model is the Hugging Face repository (default DefaultModel).
	Model string
	// Download fetches the model files.
	Download bool
	// Out receives progress and the output of Python and pip.
	Out io.Writer
	// Progress reports downloads (nil: none).
	Progress func(file string, done, total int64)
}

// Install sets up the tagger in opts.Dir: a Python virtual environment
// with the requirements, the server script and, with Download, the model.
// Running it again updates the installation.
func Install(ctx context.Context, opts InstallOptions) (Installation, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	say := func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) }
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Installation{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Installation{}, err
	}
	prev, _ := readInfo(dir)
	repo := opts.Model
	if repo == "" {
		repo = prev.Model
	}
	if repo == "" {
		repo = DefaultModel
	}
	if strings.Count(repo, "/") != 1 {
		return Installation{}, fmt.Errorf("the model must be a Hugging Face repository such as %s", DefaultModel)
	}
	if prev.Model != "" && prev.Model != repo {
		for _, f := range []string{ModelFile, TagsFile} {
			os.Remove(filepath.Join(dir, f))
		}
	}

	// Device.
	device := opts.Device
	switch device {
	case "", "auto":
		if gpu, ok := DetectGPU(ctx); ok {
			device = "cuda"
			say("Found %s (driver %s, %s): using CUDA.", gpu.Name, gpu.Driver, gpu.Memory)
		} else {
			device = "cpu"
			say("No NVIDIA GPU found (nvidia-smi): the tagger will run on the CPU, which is slower.")
		}
	case "cuda", "cpu":
	default:
		return Installation{}, fmt.Errorf("the device must be auto, cuda or cpu")
	}
	pipCUDA := device == "cuda" && !opts.SystemCUDA

	// Python environment: reused when it works.
	vpy := VenvPython(dir)
	if opts.Fresh {
		if err := os.RemoveAll(filepath.Join(dir, venvDir)); err != nil {
			return Installation{}, err
		}
	}
	base, reuse := Python{}, false
	if _, err := os.Stat(vpy); err == nil {
		if p, err := probePython(ctx, vpy); err == nil {
			base, reuse = p, true
			say("Using the existing Python environment (Python %s).", p.Version)
		} else {
			say("The existing Python environment does not work; creating it again.")
			if err := os.RemoveAll(filepath.Join(dir, venvDir)); err != nil {
				return Installation{}, err
			}
		}
	}
	if !reuse {
		if base, err = choosePython(ctx, opts.Python); err != nil {
			return Installation{}, err
		}
		say("Creating a Python environment with %s…", base)
		if err := run(ctx, out, base.Path, "-m", "venv", filepath.Join(dir, venvDir)); err != nil {
			return Installation{}, fmt.Errorf("creating the Python environment: %w", err)
		}
	}

	// Script and requirements.
	if err := os.WriteFile(filepath.Join(dir, ScriptFile), script, 0o644); err != nil {
		return Installation{}, err
	}
	reqPath := filepath.Join(dir, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte(requirementsFor(device, pipCUDA)), 0o644); err != nil {
		return Installation{}, err
	}

	// Packages. onnxruntime and onnxruntime-gpu provide the same module,
	// so the other one goes when switching.
	switch {
	case pipCUDA:
		say("Installing the Python packages (onnxruntime-gpu with CUDA and cuDNN, about 1.3 GB)…")
	case device == "cuda":
		say("Installing the Python packages (onnxruntime-gpu, using the system's CUDA and cuDNN)…")
	default:
		say("Installing the Python packages (onnxruntime for the CPU)…")
	}
	if err := run(ctx, out, vpy, "-m", "pip", "install", "--disable-pip-version-check", "--upgrade", "pip"); err != nil {
		return Installation{}, fmt.Errorf("updating pip: %w", err)
	}
	other := "onnxruntime-gpu"
	if device == "cuda" {
		other = "onnxruntime"
	}
	if installed(ctx, vpy, other) {
		say("Removing %s…", other)
		if err := run(ctx, out, vpy, "-m", "pip", "uninstall", "--disable-pip-version-check", "-y", other); err != nil {
			return Installation{}, err
		}
	}
	if err := run(ctx, out, vpy, "-m", "pip", "install", "--disable-pip-version-check", "-r", reqPath); err != nil {
		return Installation{}, fmt.Errorf("installing the packages: %w", err)
	}
	vp, err := probePython(ctx, vpy)
	if err != nil {
		return Installation{}, err
	}
	res, err := checkPackages(ctx, vpy)
	if err != nil {
		if vp.Conda {
			hint := "Install Python from https://www.python.org/downloads/ and run this again with --fresh."
			for _, p := range FindPythons(ctx) {
				if !p.Conda && p.Problem == "" {
					hint = fmt.Sprintf("Run this again with --fresh to use %s instead.", p)
					break
				}
			}
			err = fmt.Errorf("%w\nThe environment uses Python from conda, which is known to cause this on Windows. %s", err, hint)
		}
		return Installation{}, err
	}
	say("onnxruntime %s", res)

	if err := writeInfo(dir, installInfo{
		Model: repo, Device: device, PipCUDA: pipCUDA, Python: vp.Version, BasePython: base.Path,
		Script: sha(script), InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return Installation{}, err
	}

	// Model.
	if opts.Download {
		if err := Download(ctx, repo, dir, out, opts.Progress); err != nil {
			return Detect(dir), err
		}
	}
	return Detect(dir), nil
}

// checkPackages imports the server's packages one by one, so a failure
// names the package.
func checkPackages(ctx context.Context, py string) (string, error) {
	for _, m := range []string{"numpy", "PIL", "fastapi", "uvicorn", "onnxruntime"} {
		out, err := hidden(exec.CommandContext(ctx, py, "-c", "import "+m)).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if lines := strings.Split(msg, "\n"); len(lines) > 6 {
				msg = strings.Join(lines[len(lines)-6:], "\n")
			}
			if msg == "" {
				msg = fmt.Sprintf("Python crashed (%v)", err)
			}
			return "", fmt.Errorf("the %s package does not load: %s", m, msg)
		}
	}
	out, err := hidden(exec.CommandContext(ctx, py, "-c", "import onnxruntime as ort; print(ort.__version__, ' '.join(ort.get_available_providers()))")).Output()
	return strings.TrimSpace(string(out)), err
}

// choosePython checks the interpreter given, or finds the best one.
func choosePython(ctx context.Context, given string) (Python, error) {
	if given != "" {
		p, err := probePython(ctx, given)
		if err != nil {
			return p, err
		}
		if p.Problem != "" {
			return p, fmt.Errorf("%s cannot be used: %s", p, p.Problem)
		}
		return p, nil
	}
	found := FindPythons(ctx)
	if len(found) > 0 && found[0].Problem == "" {
		return found[0], nil // conda last, see Python.Conda
	}
	install := fmt.Sprintf("Install Python 3.%d to 3.%d from https://www.python.org/downloads/ (or with your package manager), "+
		"or pass --python", minMinor+2, maxMinor)
	if len(found) == 0 {
		return Python{}, errors.New("no Python was found. " + install)
	}
	msg := "no usable Python was found:"
	venvOnly := true
	for _, p := range found {
		msg += fmt.Sprintf("\n  %s: %s", p, p.Problem)
		venvOnly = venvOnly && p.minor >= minMinor && strings.Contains(p.Problem, "virtual environments")
	}
	if venvOnly && runtime.GOOS == "linux" {
		return Python{}, fmt.Errorf("%s\nPython's venv module is missing; on Debian or Ubuntu: sudo apt install python3.%d-venv", msg, found[0].minor)
	}
	return Python{}, errors.New(msg + "\n" + install)
}

// installed reports whether a package is in the environment.
func installed(ctx context.Context, py, pkg string) bool {
	out, err := hidden(exec.CommandContext(ctx, py, "-m", "pip", "list", "--disable-pip-version-check", "--format=json")).Output()
	if err != nil {
		return false
	}
	var list []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(out, &list)
	for _, p := range list {
		if strings.EqualFold(p.Name, pkg) {
			return true
		}
	}
	return false
}

func run(ctx context.Context, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = out, out // a terminal is passed on, so pip shows progress bars
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

func hidden(cmd *exec.Cmd) *exec.Cmd {
	hideWindow(cmd)
	return cmd
}

// Uninstall removes an installation's files from dir, and dir when
// nothing else is left in it.
func Uninstall(dir string) error {
	for _, name := range []string{venvDir, ScriptFile, "requirements.txt", infoFile, logFile, ModelFile, TagsFile,
		ModelFile + ".part", TagsFile + ".part", "__pycache__"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		return os.Remove(dir)
	}
	return nil
}
