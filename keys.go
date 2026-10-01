package main

import "charm.land/bubbles/v2/key"

// The keys of every screen; help.go lays out the table's help.
var (
	keyForceQuit = key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "abort"))

	// The table (its cursor keys are the table's own).
	keyPick     = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "upgrade"))
	keyVersions = key.NewBinding(key.WithKeys("enter", "v"), key.WithHelp("enter/v", "version"))
	keyAll      = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "upgrade all shown"))
	keyFilter   = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keyOpen     = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open page"))
	keySave     = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save"))
	keyBack     = key.NewBinding(key.WithKeys("left", "h", "esc", "q"), key.WithHelp("←/h/esc/q", "back"))
	keyRefresh  = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))

	// The sidebar.
	keySideUp   = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	keySideDown = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	keySideOpen = key.NewBinding(key.WithKeys("enter", "right", "l"), key.WithHelp("→/enter", "open"))
	keySideQuit = key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "quit"))

	// A filter being typed.
	keyFilterKeep  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep filter"))
	keyFilterClear = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter"))

	// The version picker.
	keyMove       = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/k ↓/j", "move"))
	keyPickChoose = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))
	keyPickCancel = key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc", "cancel"))

	// The quit question.
	keyQuitYes = key.NewBinding(key.WithKeys("y", "q"), key.WithHelp("y/q", "quit"))
	keyQuitNo  = key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "stay"))

	// The review.
	keyReviewStart = key.NewBinding(key.WithKeys("enter", "s"), key.WithHelp("enter/s", "start"))
	keyReviewBack  = key.NewBinding(key.WithKeys("esc", "q", "left", "h"), key.WithHelp("esc/q", "back to the list"))
	keyScroll      = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/k ↓/j", "scroll"))

	// The progress screen, once the run is over.
	keyDoneBack = key.NewBinding(key.WithKeys("enter", "esc"), key.WithHelp("enter/esc", "back to the list"))
	keyDoneQuit = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
)
