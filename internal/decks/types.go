package decks

// How slides advance.
const (
	AdvanceManual = "manual" // on a key press, click or swipe
	AdvanceTimed  = "timed"  // every Interval seconds
)

// How an image fills the screen.
const (
	FitContain = "contain" // the whole image, as large as fits
	FitCover   = "cover"   // fills the screen, cropping the edges
	FitStretch = "stretch" // fills the screen, distorting the image
	FitCenter  = "center"  // actual size, centred (larger images shrink to fit)
)

// Settings control how a slideshow plays.
type Settings struct {
	Advance string `json:"advance" tstype:"'manual' | 'timed'"`
	// Interval is how many seconds each slide shows when timed.
	Interval float64 `json:"interval"`
	// Crossfade blends each slide into the next over Fade seconds;
	// otherwise slides cut. Fade is kept while cross-fading is off.
	Crossfade bool    `json:"crossfade"`
	Fade      float64 `json:"fade"`
	Fit       string  `json:"fit" tstype:"'contain' | 'cover' | 'stretch' | 'center'"`
	// Background is the colour around (and behind transparent) images, as
	// #rrggbb.
	Background string `json:"background"`
	// Loop starts over after the last slide.
	Loop bool `json:"loop"`
	// Shuffle plays the slides in a random order.
	Shuffle bool `json:"shuffle"`
	// Captions shows each image's description, or its name if it has none.
	Captions bool `json:"captions"`
}

// Deck is a slide deck.
type Deck struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Notes    string   `json:"notes"`
	Settings Settings `json:"settings"`
	// Count is the number of slides. Hidden counts images of the deck that
	// are in the trash; they return to their place if restored.
	Count  int `json:"count"`
	Hidden int `json:"hidden"`
	// Covers are the first few images.
	Covers    []int64 `json:"covers"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

// Detail is a deck with its images in slide order.
type Detail struct {
	Deck `tstype:",extends"`
	IDs  []int64 `json:"ids"`
}

// Ref names a deck.
type Ref struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Added reports images added to a deck.
type Added struct {
	Added int `json:"added"`
	// Present were in the deck already.
	Present int `json:"present"`
}
