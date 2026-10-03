package reencode

// Settings describe how a batch re-encodes its images.
type Settings struct {
	// Format is "jpeg", "png" or "webp".
	Format string `json:"format" tstype:"\"jpeg\" | \"png\" | \"webp\""`
	// Lossless selects lossless WebP; PNG is always lossless, JPEG never.
	Lossless bool `json:"lossless"`
	// Quality (1..100) applies to JPEG and lossy WebP.
	Quality int `json:"quality"`
	// Effort trades time for size: WebP 0..6, PNG 0..2.
	Effort int `json:"effort"`
	// Progressive and Chroma444 apply to JPEG.
	Progressive bool `json:"progressive"`
	Chroma444   bool `json:"chroma444"`
	// MaxWidth and MaxHeight scale larger images down to fit, keeping
	// their proportions (0: no limit).
	MaxWidth  int `json:"maxWidth"`
	MaxHeight int `json:"maxHeight"`
	// KeepMetadata carries EXIF and XMP over; the colour profile always is.
	KeepMetadata bool `json:"keepMetadata"`
	// OnlySmaller keeps the original when the new file is not smaller.
	OnlySmaller bool `json:"onlySmaller"`
}

// Modes.
const (
	// ModeReplace replaces each original as soon as its result is made.
	ModeReplace = "replace"
	// ModeReview keeps results until the user compares and decides.
	ModeReview = "review"
)

// Batch states.
const (
	StateQueued   = "queued"
	StateRunning  = "running"
	StateStopped  = "stopped" // cancelled or interrupted, with images left
	StateFinished = "finished"
)

// Item statuses.
const (
	Pending  = "pending"
	Ready    = "ready" // awaiting review
	Replaced = "replaced"
	Kept     = "kept" // the user kept the original
	Skipped  = "skipped"
	Failed   = "failed"
)

// Counts tallies a batch's items by status.
type Counts struct {
	Total    int `json:"total"`
	Pending  int `json:"pending"`
	Ready    int `json:"ready"`
	Replaced int `json:"replaced"`
	Kept     int `json:"kept"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// Batch is a re-encode of a set of images.
type Batch struct {
	ID          int64    `json:"id"`
	Settings    Settings `json:"settings"`
	Mode        string   `json:"mode" tstype:"\"replace\" | \"review\""`
	Description string   `json:"description"`
	State       string   `json:"state" tstype:"\"queued\" | \"running\" | \"stopped\" | \"finished\""`
	Error       string   `json:"error,omitempty"`
	JobID       string   `json:"jobId,omitempty"`
	Counts      Counts   `json:"counts"`
	// ReadyBytes are the originals and results awaiting review.
	ReadyOld int64 `json:"readyOld"`
	ReadyNew int64 `json:"readyNew"`
	// ReplacedOld and ReplacedNew are the sizes before and after the
	// replacements made.
	ReplacedOld int64 `json:"replacedOld"`
	ReplacedNew int64 `json:"replacedNew"`
	CreatedAt   int64 `json:"createdAt"`
	FinishedAt  int64 `json:"finishedAt,omitempty"`
}

// Item is one image of a batch.
type Item struct {
	ImageID int64  `json:"imageId"`
	Name    string `json:"name"`
	Ord     int    `json:"ord"`
	Status  string `json:"status" tstype:"\"pending\" | \"ready\" | \"replaced\" | \"kept\" | \"skipped\" | \"failed\""`
	// Reason explains a skip or failure.
	Reason string `json:"reason,omitempty"`
	// Notes explain changes besides the encoding (a dropped profile...).
	Notes     []string `json:"notes"`
	OldFormat string   `json:"oldFormat"`
	OldSize   int64    `json:"oldSize"`
	OldWidth  int      `json:"oldWidth"`
	OldHeight int      `json:"oldHeight"`
	NewFormat string   `json:"newFormat,omitempty"`
	NewSize   int64    `json:"newSize,omitempty"`
	NewWidth  int      `json:"newWidth,omitempty"`
	NewHeight int      `json:"newHeight,omitempty"`
	// NewSHA256 names the result's thumbnail (/api/thumbs/{sha}).
	NewSHA256 string `json:"newSha256,omitempty"`
	// PSNR compares the result with the original's pixels (dB); absent
	// when they are identical.
	PSNR   *float64 `json:"psnr,omitempty"`
	Millis int64    `json:"millis"`
}

// Detail is a batch with its items.
type Detail struct {
	Batch `tstype:",extends"`
	Items []Item `json:"items"`
}

// Progress is reported while a batch runs.
type Progress struct {
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	Ready    int    `json:"ready"`
	Replaced int    `json:"replaced"`
	Skipped  int    `json:"skipped"`
	Failed   int    `json:"failed"`
	OldBytes int64  `json:"oldBytes"`
	NewBytes int64  `json:"newBytes"`
	Current  string `json:"current,omitempty"`
}

// Decided reports decisions taken.
type Decided struct {
	Replaced int `json:"replaced"`
	Kept     int `json:"kept"`
	// Stale counts images changed since their result was made, whose
	// results were dropped.
	Stale int `json:"stale"`
}

// Change is a replacement in an image's history.
type Change struct {
	OldFormat string `json:"oldFormat"`
	OldSize   int64  `json:"oldSize"`
	OldWidth  int    `json:"oldWidth"`
	OldHeight int    `json:"oldHeight"`
	NewFormat string `json:"newFormat"`
	NewSize   int64  `json:"newSize"`
	NewWidth  int    `json:"newWidth"`
	NewHeight int    `json:"newHeight"`
	BatchID   int64  `json:"batchId,omitempty"`
	At        int64  `json:"at"`
}

// Estimate is the outcome of trying settings on a few images.
type Estimate struct {
	Tried    int      `json:"tried"`
	OldBytes int64    `json:"oldBytes"`
	NewBytes int64    `json:"newBytes"`
	MinPSNR  *float64 `json:"minPsnr,omitempty"`
	Exact    bool     `json:"exact"`
	Millis   int64    `json:"millis"`
	Problems []string `json:"problems"`
}
