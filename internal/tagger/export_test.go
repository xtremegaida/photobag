package tagger

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// SetHFBase points downloads at a fake Hugging Face.
func SetHFBase(u string) (restore func()) {
	old := hfBase
	hfBase = u
	return func() { hfBase = old }
}

// fakeEnv makes the test binary act as the tagger server when the local
// tagger starts it as "python": serve, fail or slow.
const fakeEnv = "PHOTOBAG_FAKE_TAGGER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		fakeServer(mode, os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// fakeServer imitates tagger_server.py: python -u tagger_server.py --host
// H --port P --device D.
func fakeServer(mode string, args []string) {
	var host, port, device string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--host":
			host = args[i+1]
		case "--port":
			port = args[i+1]
		case "--device":
			device = args[i+1]
		}
	}
	fmt.Println("Loaded 3 tags")
	switch mode {
	case "fail":
		fmt.Fprintln(os.Stderr, "RuntimeError: CUDAExecutionProvider is not available.")
		os.Exit(1)
	case "slow":
		time.Sleep(time.Second)
	}
	for _, env := range []string{"MODEL_PATH", "TAGS_PATH"} {
		if _, err := os.Stat(os.Getenv(env)); err != nil {
			fmt.Fprintln(os.Stderr, env, "missing:", err)
			os.Exit(2)
		}
	}
	providers := []string{"CUDAExecutionProvider", "CPUExecutionProvider"}
	if device == "cpu" {
		providers = providers[1:]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "name": os.Getenv("MODEL_NAME"), "model": os.Getenv("MODEL_PATH"),
			"providers": providers, "tag_count": 3})
	})
	mux.HandleFunc("POST /tag/details", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"general":[{"tag":"sky","score":0.9}],"characters":[],"rating":{"tag":"general","score":0.99}}`)
	})
	mux.HandleFunc("GET /crash", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Segmentation fault")
		os.Exit(3)
	})
	ln, err := net.Listen("tcp", host+":"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Uvicorn running on http://" + strings.TrimSpace(ln.Addr().String()))
	http.Serve(ln, mux)
}

// FastRetries shortens a client's waits between retries.
func FastRetries(c *Client) *Client {
	c.delay = time.Millisecond
	return c
}
