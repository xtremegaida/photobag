package comfy

// Device is a compute device of the server.
type Device struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	VRAMTotal int64  `json:"vramTotal"`
	VRAMFree  int64  `json:"vramFree"`
}

// ServerInfo describes a ComfyUI server.
type ServerInfo struct {
	Version string   `json:"version"`
	OS      string   `json:"os"`
	Python  string   `json:"python"`
	Pytorch string   `json:"pytorch"`
	Devices []Device `json:"devices"`
	// Queue is how many prompts it is running or has waiting.
	Queue int `json:"queue"`
	// WebsocketNode reports whether "Send Image (WebSocket)" is installed.
	WebsocketNode bool `json:"websocketNode"`
}

// InputSpec describes a node input, from ComfyUI's node definitions.
type InputSpec struct {
	Name string `json:"name"`
	// Type is INT, FLOAT, STRING, BOOLEAN, COMBO or a connection type
	// such as MODEL or IMAGE.
	Type      string   `json:"type"`
	Options   []any    `json:"options,omitempty"`
	Default   any      `json:"default,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Step      *float64 `json:"step,omitempty"`
	Multiline bool     `json:"multiline,omitempty"`
	// Seed marks inputs ComfyUI randomises after each run.
	Seed    bool   `json:"seed,omitempty"`
	Tooltip string `json:"tooltip,omitempty"`
}

// NodeInfo describes a node class.
type NodeInfo struct {
	Class       string      `json:"class"`
	DisplayName string      `json:"displayName"`
	Inputs      []InputSpec `json:"inputs"`
	OutputNode  bool        `json:"outputNode"`
}
