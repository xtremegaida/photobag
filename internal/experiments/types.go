// Package experiments keeps ComfyUI workflow templates and generation
// experiments in a bag. Generated images are held in their experiment,
// apart from the library, until they are moved there or discarded; each
// keeps the workflow version and the values it was made with.
package experiments

import "photobag/internal/comfy"

// Settings are the ComfyUI connection, stored in the bag.
type Settings struct {
	// Endpoint is ComfyUI's address, e.g. http://127.0.0.1:8188.
	Endpoint string `json:"endpoint"`
}

// Workflow is a saved workflow template.
type Workflow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Notes     string `json:"notes"`
	VersionID int64  `json:"versionId"`
	NodeCount int    `json:"nodeCount"`
	// Output is how the workflow returns images: "websocket", "file" or
	// "" (it cannot).
	Output    string `json:"output"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Version is the content of a workflow as it was at some point.
type Version struct {
	ID        int64        `json:"id"`
	JSON      string       `json:"json"`
	Nodes     []comfy.Node `json:"nodes"`
	Problems  []string     `json:"problems"`
	Classes   []string     `json:"classes"`
	Output    string       `json:"output"`
	CreatedAt int64        `json:"createdAt"`
}

// WorkflowDetail is a template with its current content.
type WorkflowDetail struct {
	Workflow Workflow `json:"workflow"`
	Version  Version  `json:"version"`
}

// Request is what to generate.
type Request struct {
	WorkflowID int64 `json:"workflowId"`
	// VersionID pins an exact workflow version (when picking up from an
	// image); 0 uses the template's current one.
	VersionID int64            `json:"versionId,omitempty"`
	Overrides []comfy.Override `json:"overrides"`
	// Count is how many images to make of each combination of swept
	// values (each with its own seed).
	Count int `json:"count"`
	// Seed is the seed policy: "random", "fixed" or "increment".
	Seed string `json:"seed"`
}

// Experiment is a container for generated images.
type Experiment struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Notes string  `json:"notes"`
	Last  Request `json:"request"`
	// Held counts the images still in the experiment; Moved those moved
	// to the library.
	Held      int   `json:"held"`
	Moved     int   `json:"moved"`
	Runs      int   `json:"runs"`
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
	// Covers are the SHA-256 of a few recent images, for thumbnails.
	Covers []string `json:"covers"`
}

// Run statuses.
const (
	RunRunning   = "running"
	RunDone      = "done"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// Run is one press of Generate.
type Run struct {
	ID           int64       `json:"id"`
	ExperimentID int64       `json:"experimentId"`
	VersionID    int64       `json:"versionId"`
	WorkflowID   int64       `json:"workflowId,omitempty"`
	WorkflowName string      `json:"workflowName"`
	Request      Request     `json:"request"`
	Dims         []comfy.Dim `json:"dims"`
	Prompts      int         `json:"prompts"`
	Done         int         `json:"done"`
	Failed       int         `json:"failed"`
	Images       int         `json:"images"`
	Status       string      `json:"status"`
	Error        string      `json:"error,omitempty"`
	Failures     []Failure   `json:"failures"`
	JobID        string      `json:"jobId,omitempty"`
	CreatedAt    int64       `json:"createdAt"`
	FinishedAt   int64       `json:"finishedAt,omitempty"`
}

// Failure is a prompt that made no image.
type Failure struct {
	Combo  int             `json:"combo"`
	Repeat int             `json:"repeat"`
	Values []comfy.Applied `json:"values"`
	Error  string          `json:"error"`
}

// Generation is a generated image.
type Generation struct {
	ID           int64  `json:"id"`
	ExperimentID int64  `json:"experimentId,omitempty"`
	RunID        int64  `json:"runId,omitempty"`
	VersionID    int64  `json:"versionId"`
	WorkflowID   int64  `json:"workflowId,omitempty"`
	WorkflowName string `json:"workflowName"`
	// Applied are the values set on the workflow version: fixed overrides,
	// swept values (kind "sweep") and seeds (kind "seed").
	Applied    []comfy.Applied `json:"applied"`
	Combo      int             `json:"combo"`
	Repeat     int             `json:"repeat"`
	BatchIndex int             `json:"batchIndex"`
	Seed       *int64          `json:"seed,omitempty"`
	SHA256     string          `json:"sha256"`
	Format     string          `json:"format"`
	Size       int64           `json:"size"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	ThumbW     int             `json:"thumbW"`
	ThumbH     int             `json:"thumbH"`
	Millis     int64           `json:"millis"`
	CreatedAt  int64           `json:"createdAt"`
	// ImageID is the library image once moved there (0 while held).
	ImageID int64 `json:"imageId,omitempty"`
	// ExperimentName names the experiment (empty once it is deleted).
	ExperimentName string `json:"experimentName,omitempty"`
}

// Progress reports a generation job.
type Progress struct {
	ExperimentID int64 `json:"experimentId"`
	RunID        int64 `json:"runId"`
	Prompts      int   `json:"prompts"`
	Done         int   `json:"done"`
	Failed       int   `json:"failed"`
	Images       int   `json:"images"`
	// Current describes the running prompt's swept values and repeat.
	Current string `json:"current,omitempty"`
	Node    string `json:"node,omitempty"`
	Step    int    `json:"step,omitempty"`
	Steps   int    `json:"steps,omitempty"`
	// Preview counts sampler previews; the latest can be fetched.
	Preview   int    `json:"preview,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

// Result is a finished generation job.
type Result struct {
	ExperimentID int64 `json:"experimentId"`
	RunID        int64 `json:"runId"`
	Prompts      int   `json:"prompts"`
	Images       int   `json:"images"`
	Failed       int   `json:"failed"`
}

// PlanSummary previews a request.
type PlanSummary struct {
	Prompts    int         `json:"prompts"`
	Combos     int         `json:"combos"`
	Count      int         `json:"count"`
	Dims       []comfy.Dim `json:"dims"`
	SeedInputs []string    `json:"seedInputs"`
	Warnings   []string    `json:"warnings"`
	VersionID  int64       `json:"versionId"`
	// Error is why the request cannot run as it is.
	Error string `json:"error,omitempty"`
}

// MoveResult reports images moved to the library.
type MoveResult struct {
	Moved    int     `json:"moved"`
	ImageIDs []int64 `json:"imageIds"`
}
