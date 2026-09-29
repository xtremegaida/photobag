package tagger

// Installation describes a local tagger installation.
type Installation struct {
	Dir string `json:"dir"`
	// Installed means the Python environment and the script are there.
	Installed bool `json:"installed"`
	// ModelReady means model.onnx and selected_tags.csv are there.
	ModelReady bool `json:"modelReady"`
	Ready      bool `json:"ready"`
	// Model is the Hugging Face repository the model comes from.
	Model string `json:"model,omitempty"`
	// Device is cuda or cpu, as installed.
	Device      string `json:"device,omitempty"`
	Python      string `json:"python,omitempty"`
	ModelBytes  int64  `json:"modelBytes"`
	InstalledAt string `json:"installedAt,omitempty"`
	// Problem says what is missing and how to fix it.
	Problem string `json:"problem,omitempty"`
}

// LocalStatus reports the local tagger.
type LocalStatus struct {
	Installation Installation `json:"installation"`
	// State is stopped, starting or running.
	State     string   `json:"state"`
	Port      int      `json:"port,omitempty"`
	OnGPU     bool     `json:"onGpu"`
	Providers []string `json:"providers"`
	// StartedAt is when it became ready (Unix ms).
	StartedAt int64 `json:"startedAt,omitempty"`
	// IdleMinutes is how long it runs unused before stopping.
	IdleMinutes int `json:"idleMinutes"`
	// LastError is why it last failed to start or stopped unexpectedly.
	LastError string `json:"lastError,omitempty"`
}
