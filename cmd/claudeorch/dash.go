package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"os"
	"os/exec"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/DhilipBinny/claudeorch/internal/mux"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/session"
	"github.com/DhilipBinny/claudeorch/internal/usage"
	"github.com/DhilipBinny/claudeorch/internal/watch"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newDashCmd())
	})
}

func newDashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dash",
		Short: "Interactive TUI dashboard for all Claude sessions.",
		Long: `Opens a live terminal dashboard showing all claudeorch tmux sessions,
their state (active/idle), and recent output. k9s-inspired interface.

Views (switch with 1-4 or :command):
  1  Sessions   — live session/window list with state + preview
  2  Profiles   — all profiles with usage bars + session counts
  3  Activity   — scrollable chronological state-change log
  4  Help       — full keybinding reference

Features: command palette (:), live tail, inline send, toast messages.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mux.EnsureTmux(); err != nil {
				return err
			}
			p := tea.NewProgram(newDashModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
			_, err := p.Run()
			return err
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}

// ── types ──────────────────────────────────────────────────────────────

type dashView int

const (
	viewSessions dashView = iota
	viewProfiles
	viewActivity
	viewHistory
	viewHelp
)

type dashOverlay int

const (
	overlayNone dashOverlay = iota
	overlayPeek
	overlaySend
	overlayConfirmKill
	overlayConfirmBatchKill
	overlayNewSession
	overlayAddWindow
	overlayCommandPalette
	overlaySwapConfirm
)

type windowEntry struct {
	session      string
	profile      string
	window       mux.Window
	state        watch.State
	idleSince    time.Time
	selected     bool
	lastLine     string // output preview
	stateChanged time.Time
	attached     int
}

type activityEntry struct {
	timestamp time.Time
	session   string
	window    int
	profile   string
	event     string
	stateFrom watch.State
	stateTo   watch.State
}

type profileEntry struct {
	name         string
	email        string
	source       string
	location     string
	active       bool
	usage5h      float64
	usage7d      float64
	reset5h      string
	reset7d      string
	sessionCount int
}

type toastMsg struct {
	text    string
	expires time.Time
}

type dashModel struct {
	// Views
	view    dashView
	overlay dashOverlay

	// Session view
	entries    []windowEntry
	cursor     int
	prevStates map[string]watch.State

	// Profile view
	profiles   []profileEntry
	profCursor int

	// Activity log
	activities []activityEntry
	actCursor  int

	// History view
	history    []session.Conversation
	histCursor int
	histLoaded bool

	// Overlays
	peek        viewport.Model
	peekTarget  string // "session:window" for live tail
	input       textinput.Model
	sendHistory []string
	histIdx     int
	peekSend    bool // inline send from peek

	// Command palette
	cmdInput textinput.Model

	// New session form
	newName    textinput.Model
	newProfile textinput.Model
	newCwd     textinput.Model
	newExtra   textinput.Model
	newField   int

	// Add window form
	addWinCwd textinput.Model

	// Swap confirm
	swapTarget string

	// Filter
	filterActive bool
	filter       textinput.Model

	// Activity filter
	actFilterActive bool
	actFilter       textinput.Model

	// Command palette suggestions
	cmdSuggestions []string
	cmdSugIdx      int

	// Toast
	toasts []toastMsg

	// Dimensions
	width  int
	height int

	// Meta
	startTime   time.Time
	lastRefresh time.Time
	tickCount   int
}

// ── messages ───────────────────────────────────────────────────────────

type tickMsg time.Time
type quickLoadMsg struct {
	entries []windowEntry
}
type refreshMsg struct {
	entries  []windowEntry
	profiles []profileEntry
}
type peekTickMsg time.Time
type toastExpireMsg struct{}
type histResumeReturnMsg struct{}

// ── init ───────────────────────────────────────────────────────────────

func newDashModel() dashModel {
	ti := textinput.New()
	ti.Placeholder = "Type a message to send..."
	ti.CharLimit = 500

	fi := textinput.New()
	fi.Placeholder = "Filter sessions..."
	fi.CharLimit = 100

	ci := textinput.New()
	ci.Placeholder = "command..."
	ci.Prompt = ":"
	ci.CharLimit = 200

	nn := textinput.New()
	nn.Placeholder = "Session name"
	nn.CharLimit = 50

	np := textinput.New()
	np.Placeholder = "Profile name"
	np.CharLimit = 50

	nc := textinput.New()
	nc.Placeholder = "Working directory (optional)"
	nc.CharLimit = 200

	ne := textinput.New()
	ne.Placeholder = "e.g. -- agents, --model opus (optional)"
	ne.CharLimit = 200

	aw := textinput.New()
	aw.Placeholder = "Working directory for new window (optional)"
	aw.CharLimit = 200

	af := textinput.New()
	af.Placeholder = "Filter activity..."
	af.CharLimit = 100

	vp := viewport.New(80, 20)

	return dashModel{
		input:      ti,
		filter:     fi,
		actFilter:  af,
		cmdInput:   ci,
		peek:       vp,
		newName:    nn,
		newProfile: np,
		newCwd:     nc,
		newExtra:   ne,
		addWinCwd:  aw,
		prevStates: make(map[string]watch.State),
		startTime:  time.Now(),
	}
}

func (m dashModel) Init() tea.Cmd {
	return tea.Batch(
		dashQuickLoadCmd(),
		dashRefreshCmd(),
		dashTickCmd(),
	)
}

func dashQuickLoadCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, _ := mux.ListSessions()
		var entries []windowEntry
		for _, s := range sessions {
			for _, w := range s.Windows {
				entries = append(entries, windowEntry{
					session:  s.Name,
					profile:  s.Profile,
					window:   w,
					attached: s.Attached,
				})
			}
		}
		return quickLoadMsg{entries: entries}
	}
}

func dashTickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func peekTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return peekTickMsg(t)
	})
}

func toastExpireCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return toastExpireMsg{}
	})
}

func dashRefreshCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, _ := mux.ListSessions()

		profileSessionCounts := make(map[string]int)
		var entries []windowEntry

		for _, s := range sessions {
			for _, w := range s.Windows {
				state, _ := watch.DetectState(s.Name, w.Index)

				var lastLine string
				if raw, err := mux.CapturePaneOutput(s.Name, w.Index, 5); err == nil {
					lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
					for i := len(lines) - 1; i >= 0; i-- {
						stripped := strings.TrimSpace(watch.StripANSI(lines[i]))
						if stripped != "" && stripped != "❯" && stripped != ">" {
							lastLine = stripped
							break
						}
					}
				}

				entries = append(entries, windowEntry{
					session:  s.Name,
					profile:  s.Profile,
					window:   w,
					state:    state,
					lastLine: lastLine,
					attached: s.Attached,
				})
				profileSessionCounts[s.Profile]++
			}
		}

		var profiles []profileEntry
		storePath, err := paths.StoreFile()
		if err == nil {
			store, err := profile.Load(storePath)
			if err == nil {
				for name, p := range store.Profiles {
					pe := profileEntry{
						name:         name,
						email:        p.Email,
						location:     string(p.Location),
						active:       store.Active != nil && *store.Active == name,
						sessionCount: profileSessionCounts[name],
					}
					if p.Source == profile.SourceAPIKey {
						pe.source = "api-key"
					} else {
						pe.source = "oauth"
					}
					if u := usage.LoadCached(name); u != nil {
						pe.usage5h = u.FiveHour.Percent
						pe.usage7d = u.SevenDay.Percent
						if !u.FiveHour.ResetsAt.IsZero() {
							pe.reset5h = shortDuration(time.Until(u.FiveHour.ResetsAt))
						}
						if !u.SevenDay.ResetsAt.IsZero() {
							pe.reset7d = shortDuration(time.Until(u.SevenDay.ResetsAt))
						}
					}
					profiles = append(profiles, pe)
				}
				sort.Slice(profiles, func(i, j int) bool {
					if profiles[i].active != profiles[j].active {
						return profiles[i].active
					}
					return profiles[i].name < profiles[j].name
				})
			}
		}

		return refreshMsg{entries: entries, profiles: profiles}
	}
}

// ── update ─────────────────────────────────────────────────────────────

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.peek.Width = msg.Width - 6
		m.peek.Height = msg.Height - 8
		return m, nil

	case tickMsg:
		m.tickCount++
		m.expireToasts()
		return m, tea.Batch(dashRefreshCmd(), dashTickCmd())

	case peekTickMsg:
		if m.overlay == overlayPeek && m.peekTarget != "" {
			parts := strings.SplitN(m.peekTarget, ":", 2)
			if len(parts) == 2 {
				var idx int
				fmt.Sscanf(parts[1], "%d", &idx)
				if content, err := mux.CapturePaneOutput(parts[0], idx, 200); err == nil {
					m.peek.SetContent(content)
					m.peek.GotoBottom()
				}
			}
			return m, peekTickCmd()
		}
		return m, nil

	case quickLoadMsg:
		if len(m.entries) == 0 {
			m.entries = msg.entries
		}
		return m, nil

	case itermTabResultMsg:
		if msg.err != nil {
			toastCmd := m.addToast("iTerm2 tab failed: " + msg.err.Error())
			return m, toastCmd
		}
		toastCmd := m.addToast("Opened in new iTerm2 tab")
		return m, toastCmd

	case toastExpireMsg:
		m.expireToasts()
		return m, nil

	case histResumeReturnMsg:
		return m, dashRefreshCmd()

	case refreshMsg:
		m.lastRefresh = time.Now()
		for _, e := range msg.entries {
			key := fmt.Sprintf("%s:%d", e.session, e.window.Index)
			if prev, ok := m.prevStates[key]; ok && prev != e.state {
				m.activities = append(m.activities, activityEntry{
					timestamp: time.Now(),
					session:   e.session,
					window:    e.window.Index,
					profile:   e.profile,
					event:     fmt.Sprintf("%s → %s", prev, e.state),
					stateFrom: prev,
					stateTo:   e.state,
				})
				if len(m.activities) > 200 {
					m.activities = m.activities[len(m.activities)-200:]
				}
			}
			m.prevStates[key] = e.state
		}

		sorted := make([]windowEntry, len(msg.entries))
		copy(sorted, msg.entries)

		// Carry forward stateChanged timestamps
		oldTimes := make(map[string]time.Time)
		for _, e := range m.entries {
			key := fmt.Sprintf("%s:%d", e.session, e.window.Index)
			if !e.stateChanged.IsZero() {
				oldTimes[key] = e.stateChanged
			}
		}

		for i, e := range sorted {
			key := fmt.Sprintf("%s:%d", e.session, e.window.Index)
			if prev, ok := m.prevStates[key]; ok && prev != e.state {
				sorted[i].stateChanged = time.Now()
			} else if t, ok := oldTimes[key]; ok {
				sorted[i].stateChanged = t
			}
		}

		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].state != sorted[j].state {
				return sorted[i].state > sorted[j].state
			}
			return sorted[i].session < sorted[j].session
		})

		m.entries = sorted
		m.profiles = msg.profiles
		if m.cursor >= len(m.entries) && len(m.entries) > 0 {
			m.cursor = len(m.entries) - 1
		}
		return m, nil

	case tea.KeyMsg:
		if m.filterActive {
			return m.updateFilter(msg)
		}
		if m.actFilterActive {
			return m.updateActFilter(msg)
		}
		if m.overlay != overlayNone {
			return m.updateOverlay(msg)
		}
		return m.updateMain(msg)
	}

	return m, nil
}

func (m *dashModel) expireToasts() {
	now := time.Now()
	var live []toastMsg
	for _, t := range m.toasts {
		if now.Before(t.expires) {
			live = append(live, t)
		}
	}
	m.toasts = live
}

func (m *dashModel) addToast(text string) tea.Cmd {
	m.toasts = append(m.toasts, toastMsg{
		text:    text,
		expires: time.Now().Add(3 * time.Second),
	})
	return toastExpireCmd()
}

func (m dashModel) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	views := []dashView{viewSessions, viewProfiles, viewActivity, viewHistory, viewHelp}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "1":
		m.view = viewSessions
		return m, nil
	case "2":
		m.view = viewProfiles
		return m, nil
	case "3":
		m.view = viewActivity
		return m, nil
	case "4":
		if !m.histLoaded {
			m.loadHistory()
		}
		m.view = viewHistory
		return m, nil
	case "5", "?":
		m.view = viewHelp
		return m, nil
	case "right", "tab":
		for i, v := range views {
			if v == m.view {
				next := views[(i+1)%len(views)]
				if next == viewHistory && !m.histLoaded {
					m.loadHistory()
				}
				m.view = next
				return m, nil
			}
		}
	case "left", "shift+tab":
		for i, v := range views {
			if v == m.view {
				prev := views[(i+len(views)-1)%len(views)]
				if prev == viewHistory && !m.histLoaded {
					m.loadHistory()
				}
				m.view = prev
				return m, nil
			}
		}
	case "r":
		return m, dashRefreshCmd()
	case "/", "f":
		if m.view == viewSessions {
			m.filterActive = true
			m.filter.Reset()
			m.filter.Focus()
			return m, nil
		}
	case ":":
		m.cmdInput.Reset()
		m.cmdInput.Focus()
		m.overlay = overlayCommandPalette
		return m, nil
	}

	switch m.view {
	case viewSessions:
		return m.updateSessions(msg)
	case viewProfiles:
		return m.updateProfiles(msg)
	case viewActivity:
		return m.updateActivity(msg)
	case viewHistory:
		return m.updateHistory(msg)
	}

	return m, nil
}

func (m dashModel) updateSessions(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredEntries()

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(filtered)-1 {
			m.cursor++
		}
	case "g":
		m.cursor = 0
	case "G":
		if len(filtered) > 0 {
			m.cursor = len(filtered) - 1
		}

	case "enter", "p":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			e := filtered[m.cursor]
			content, err := mux.CapturePaneOutput(e.session, e.window.Index, 200)
			if err == nil {
				m.peek.SetContent(content)
				m.peek.GotoBottom()
				m.overlay = overlayPeek
				m.peekTarget = fmt.Sprintf("%s:%d", e.session, e.window.Index)
				m.peekSend = false
				return m, peekTickCmd()
			}
		}

	case "s":
		if len(filtered) > 0 {
			m.input.Reset()
			m.input.Placeholder = "Type a message to send..."
			m.input.Focus()
			m.overlay = overlaySend
		}

	case "b":
		if len(filtered) > 0 {
			m.input.Reset()
			m.input.Placeholder = "Broadcast to all windows in session..."
			m.input.Focus()
			m.overlay = overlaySend
		}

	case "a":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			e := filtered[m.cursor]
			tmuxTarget := mux.SessionName(e.session)
			if isIterm() {
				return m, openInItermTab("tmux attach-session -t " + tmuxTarget)
			}
			cmd := exec.Command("tmux", "attach-session", "-t", tmuxTarget)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return histResumeReturnMsg{}
			})
		}

	case "A":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			e := filtered[m.cursor]
			cmd := exec.Command("tmux", "attach-session", "-t", mux.SessionName(e.session))
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return histResumeReturnMsg{}
			})
		}

	case "d":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			m.overlay = overlayConfirmKill
		}

	case "n":
		prefillProfile := ""
		if len(filtered) > 0 && m.cursor < len(filtered) {
			prefillProfile = filtered[m.cursor].profile
		}
		m.openNewSession(prefillProfile)

	case "w":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			m.addWinCwd.Reset()
			m.addWinCwd.Focus()
			m.overlay = overlayAddWindow
		}

	case "D":
		selected := m.selectedEntries()
		if len(selected) > 0 {
			m.overlay = overlayConfirmBatchKill
		}

	case " ":
		if len(filtered) > 0 && m.cursor < len(filtered) {
			for i := range m.entries {
				if m.entries[i].session == filtered[m.cursor].session &&
					m.entries[i].window.Index == filtered[m.cursor].window.Index {
					m.entries[i].selected = !m.entries[i].selected
					break
				}
			}
			if m.cursor < len(filtered)-1 {
				m.cursor++
			}
		}

	case "esc":
		for i := range m.entries {
			m.entries[i].selected = false
		}
	}

	return m, nil
}

func (m dashModel) updateProfiles(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.profCursor > 0 {
			m.profCursor--
		}
	case "down", "j":
		if m.profCursor < len(m.profiles)-1 {
			m.profCursor++
		}
	case "enter":
		if m.profCursor < len(m.profiles) {
			p := m.profiles[m.profCursor]
			if !p.active && p.location != "isolated" {
				m.swapTarget = p.name
				m.overlay = overlaySwapConfirm
			} else if p.active {
				toastCmd := m.addToast(fmt.Sprintf("%s is already active", p.name))
				return m, toastCmd
			} else {
				toastCmd := m.addToast(fmt.Sprintf("%s is isolated — stop sessions first", p.name))
				return m, toastCmd
			}
		}
	case "n":
		if m.profCursor < len(m.profiles) {
			p := m.profiles[m.profCursor]
			m.openNewSession(p.name)
			return m, nil
		}
	case "g":
		m.profCursor = 0
	case "G":
		if len(m.profiles) > 0 {
			m.profCursor = len(m.profiles) - 1
		}
	case "esc":
		m.view = viewSessions
	}
	return m, nil
}

func (m dashModel) updateActivity(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredActivities()
	switch msg.String() {
	case "up", "k":
		if m.actCursor > 0 {
			m.actCursor--
		}
	case "down", "j":
		if m.actCursor < len(filtered)-1 {
			m.actCursor++
		}
	case "g":
		m.actCursor = 0
	case "G":
		if len(filtered) > 0 {
			m.actCursor = len(filtered) - 1
		}
	case "c":
		m.activities = nil
		m.actCursor = 0
		toastCmd := m.addToast("Activity log cleared")
		return m, toastCmd
	case "/", "f":
		m.actFilterActive = true
		m.actFilter.Reset()
		m.actFilter.Focus()
	case "esc":
		if m.actFilter.Value() != "" {
			m.actFilter.SetValue("")
			m.actCursor = 0
		} else {
			m.view = viewSessions
		}
	}
	return m, nil
}

func (m dashModel) filteredActivities() []activityEntry {
	q := strings.ToLower(m.actFilter.Value())
	if q == "" {
		return m.activities
	}
	var out []activityEntry
	for _, a := range m.activities {
		if strings.Contains(strings.ToLower(a.session), q) ||
			strings.Contains(strings.ToLower(a.profile), q) ||
			strings.Contains(strings.ToLower(a.event), q) {
			out = append(out, a)
		}
	}
	return out
}

func (m dashModel) updateActFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.actFilterActive = false
		m.actFilter.SetValue("")
		m.actCursor = 0
		return m, nil
	case "enter":
		m.actFilterActive = false
		m.actFilter.Blur()
		m.actCursor = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.actFilter, cmd = m.actFilter.Update(msg)
	m.actCursor = 0
	return m, cmd
}

func (m dashModel) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayPeek:
		return m.updateOverlayPeek(msg)
	case overlaySend:
		return m.updateOverlaySend(msg)
	case overlayConfirmKill:
		return m.updateOverlayKill(msg)
	case overlayConfirmBatchKill:
		return m.updateOverlayBatchKill(msg)
	case overlayNewSession:
		return m.updateOverlayNew(msg)
	case overlayAddWindow:
		return m.updateOverlayAddWindow(msg)
	case overlayCommandPalette:
		return m.updateCommandPalette(msg)
	case overlaySwapConfirm:
		return m.updateOverlaySwap(msg)
	}
	return m, nil
}

func (m dashModel) updateOverlayPeek(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.peekSend {
		return m.updatePeekSend(msg)
	}

	switch msg.String() {
	case "q", "esc", "backspace":
		m.overlay = overlayNone
		m.peekTarget = ""
		return m, nil
	case "s":
		m.input.Reset()
		m.input.Placeholder = "Send to this window..."
		m.input.Focus()
		m.peekSend = true
		return m, nil
	case "r":
		filtered := m.filteredEntries()
		if m.cursor < len(filtered) {
			e := filtered[m.cursor]
			content, err := mux.CapturePaneOutput(e.session, e.window.Index, 200)
			if err == nil {
				m.peek.SetContent(content)
				m.peek.GotoBottom()
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.peek, cmd = m.peek.Update(msg)
	return m, cmd
}

func (m dashModel) updatePeekSend(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.peekSend = false
		return m, nil
	case "enter":
		text := m.input.Value()
		if text != "" {
			filtered := m.filteredEntries()
			if m.cursor < len(filtered) {
				e := filtered[m.cursor]
				_ = mux.SendKeys(e.session, e.window.Index, text)
				m.sendHistory = append(m.sendHistory, text)
				m.histIdx = len(m.sendHistory)
				toastCmd := m.addToast(fmt.Sprintf("Sent to %s:%d", e.session, e.window.Index))
				m.peekSend = false
				m.input.Reset()
				// Refresh peek after short delay
				return m, tea.Batch(toastCmd, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
					return peekTickMsg(t)
				}))
			}
		}
		m.peekSend = false
		return m, nil
	case "up":
		if len(m.sendHistory) > 0 && m.histIdx > 0 {
			m.histIdx--
			m.input.SetValue(m.sendHistory[m.histIdx])
			m.input.CursorEnd()
		}
		return m, nil
	case "down":
		if m.histIdx < len(m.sendHistory)-1 {
			m.histIdx++
			m.input.SetValue(m.sendHistory[m.histIdx])
			m.input.CursorEnd()
		} else if m.histIdx == len(m.sendHistory)-1 {
			m.histIdx = len(m.sendHistory)
			m.input.SetValue("")
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m dashModel) updateOverlaySend(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		m.input.Placeholder = "Type a message to send..."
		return m, nil
	case "enter":
		text := m.input.Value()
		if text != "" {
			filtered := m.filteredEntries()
			if m.cursor < len(filtered) {
				e := filtered[m.cursor]
				if m.input.Placeholder == "Broadcast to all windows in session..." {
					sessions, _ := mux.ListSessions()
					for _, s := range sessions {
						if s.Name == e.session {
							for _, w := range s.Windows {
								_ = mux.SendKeys(e.session, w.Index, text)
							}
						}
					}
					toastCmd := m.addToast(fmt.Sprintf("Broadcast to %s", e.session))
					m.sendHistory = append(m.sendHistory, text)
					m.histIdx = len(m.sendHistory)
					m.overlay = overlayNone
					m.input.Placeholder = "Type a message to send..."
					return m, tea.Batch(toastCmd, dashRefreshCmd())
				}
				_ = mux.SendKeys(e.session, e.window.Index, text)
				toastCmd := m.addToast(fmt.Sprintf("Sent to %s:%d", e.session, e.window.Index))
				m.sendHistory = append(m.sendHistory, text)
				m.histIdx = len(m.sendHistory)
				m.overlay = overlayNone
				m.input.Placeholder = "Type a message to send..."
				return m, tea.Batch(toastCmd, dashRefreshCmd())
			}
		}
		m.overlay = overlayNone
		m.input.Placeholder = "Type a message to send..."
		return m, nil
	case "up":
		if len(m.sendHistory) > 0 && m.histIdx > 0 {
			m.histIdx--
			m.input.SetValue(m.sendHistory[m.histIdx])
			m.input.CursorEnd()
		}
		return m, nil
	case "down":
		if m.histIdx < len(m.sendHistory)-1 {
			m.histIdx++
			m.input.SetValue(m.sendHistory[m.histIdx])
			m.input.CursorEnd()
		} else if m.histIdx == len(m.sendHistory)-1 {
			m.histIdx = len(m.sendHistory)
			m.input.SetValue("")
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m dashModel) updateOverlayKill(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		filtered := m.filteredEntries()
		if m.cursor < len(filtered) {
			e := filtered[m.cursor]
			_ = mux.KillWindow(e.session, e.window.Index)
			m.activities = append(m.activities, activityEntry{
				timestamp: time.Now(),
				session:   e.session,
				window:    e.window.Index,
				profile:   e.profile,
				event:     "KILLED",
			})
			toastCmd := m.addToast(fmt.Sprintf("Killed %s:%d", e.session, e.window.Index))
			m.overlay = overlayNone
			return m, tea.Batch(toastCmd, dashRefreshCmd())
		}
		m.overlay = overlayNone
		return m, dashRefreshCmd()
	case "n", "N", "esc":
		m.overlay = overlayNone
	}
	return m, nil
}

func (m dashModel) updateOverlayNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		return m, nil
	case "tab", "down":
		m.newField = (m.newField + 1) % 4
		m.focusNewField()
		return m, nil
	case "shift+tab", "up":
		m.newField = (m.newField + 3) % 4
		m.focusNewField()
		return m, nil
	case "ctrl+s":
		// Submit from any field
	case "enter":
		// If profile is pre-filled, Enter on Name submits directly
		if m.newField == 0 && m.newProfile.Value() != "" {
			// fall through to submit
		} else if m.newField < 3 {
			m.newField++
			m.focusNewField()
			return m, nil
		}
	}
	if msg.String() == "ctrl+s" || msg.String() == "enter" {
		name := m.newName.Value()
		prof := m.newProfile.Value()
		cwd := m.newCwd.Value()
		extra := m.newExtra.Value()
		if name == "" || prof == "" {
			toastCmd := m.addToast("Name and Profile are required")
			return m, toastCmd
		}
		exe, _ := resolvedExecutable()
		shellCmd := mux.BuildLaunchCmd(exe, prof, cwd, extra)
		if err := mux.CreateSession(name, prof, name+"-1", shellCmd); err != nil {
			toastCmd := m.addToast(fmt.Sprintf("Error: %v", err))
			return m, toastCmd
		}
		m.activities = append(m.activities, activityEntry{
			timestamp: time.Now(),
			session:   name,
			window:    1,
			profile:   prof,
			event:     "CREATED",
		})
		toastCmd := m.addToast(fmt.Sprintf("Created session %s", name))
		m.overlay = overlayNone
		return m, tea.Batch(toastCmd, dashRefreshCmd())
	}

	var cmd tea.Cmd
	switch m.newField {
	case 0:
		m.newName, cmd = m.newName.Update(msg)
	case 1:
		m.newProfile, cmd = m.newProfile.Update(msg)
	case 2:
		m.newCwd, cmd = m.newCwd.Update(msg)
	case 3:
		m.newExtra, cmd = m.newExtra.Update(msg)
	}
	return m, cmd
}

func (m dashModel) selectedEntries() []windowEntry {
	var out []windowEntry
	for _, e := range m.entries {
		if e.selected {
			out = append(out, e)
		}
	}
	return out
}

func (m dashModel) updateOverlayBatchKill(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		selected := m.selectedEntries()
		killed := 0
		for _, e := range selected {
			_ = mux.KillWindow(e.session, e.window.Index)
			m.activities = append(m.activities, activityEntry{
				timestamp: time.Now(),
				session:   e.session,
				window:    e.window.Index,
				profile:   e.profile,
				event:     "KILLED",
			})
			killed++
		}
		for i := range m.entries {
			m.entries[i].selected = false
		}
		toastCmd := m.addToast(fmt.Sprintf("Killed %d windows", killed))
		m.overlay = overlayNone
		return m, tea.Batch(toastCmd, dashRefreshCmd())
	case "n", "N", "esc":
		m.overlay = overlayNone
	}
	return m, nil
}

func (m dashModel) updateOverlayAddWindow(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		return m, nil
	case "enter":
		filtered := m.filteredEntries()
		if m.cursor < len(filtered) {
			e := filtered[m.cursor]
			cwd := m.addWinCwd.Value()
			exe, _ := resolvedExecutable()
			shellCmd := mux.BuildLaunchCmd(exe, e.profile, cwd, "")
			winName := fmt.Sprintf("%s-%d", e.session, len(m.entries)+1)
			_ = mux.AddWindow(e.session, winName, shellCmd)
			m.activities = append(m.activities, activityEntry{
				timestamp: time.Now(),
				session:   e.session,
				profile:   e.profile,
				event:     "CREATED",
			})
			toastCmd := m.addToast(fmt.Sprintf("Added window to %s", e.session))
			m.overlay = overlayNone
			return m, tea.Batch(toastCmd, dashRefreshCmd())
		}
		m.overlay = overlayNone
		return m, nil
	}
	var cmd tea.Cmd
	m.addWinCwd, cmd = m.addWinCwd.Update(msg)
	return m, cmd
}

func (m dashModel) updateOverlaySwap(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		target := m.swapTarget
		m.overlay = overlayNone
		exe, err := resolvedExecutable()
		if err != nil {
			toastCmd := m.addToast("Failed to resolve executable")
			return m, toastCmd
		}
		cmd := exec.Command(exe, "swap", target)
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			toastCmd := m.addToast(fmt.Sprintf("Swap failed: %s", strings.TrimSpace(string(out))))
			return m, toastCmd
		}
		toastCmd := m.addToast(fmt.Sprintf("Swapped to %s", target))
		return m, tea.Batch(toastCmd, dashRefreshCmd())
	case "n", "N", "esc":
		m.overlay = overlayNone
	}
	return m, nil
}

func (m *dashModel) openNewSession(prefillProfile string) {
	m.newName.Reset()
	m.newProfile.Reset()
	m.newCwd.Reset()
	m.newExtra.Reset()

	// Auto-suggest a session name
	base := "session"
	if prefillProfile != "" {
		base = prefillProfile
	}
	name := base
	for i := 2; m.sessionNameExists(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	m.newName.SetValue(name)

	if prefillProfile != "" {
		m.newProfile.SetValue(prefillProfile)
		m.newField = 0
		m.newName.Focus()
	} else {
		m.newField = 0
		m.newName.Focus()
	}

	home, _ := os.UserHomeDir()
	m.newCwd.SetValue(home)

	m.overlay = overlayNewSession
}

func (m dashModel) sessionNameExists(name string) bool {
	for _, e := range m.entries {
		if e.session == name {
			return true
		}
	}
	return false
}

func (m *dashModel) focusNewField() {
	m.newName.Blur()
	m.newProfile.Blur()
	m.newCwd.Blur()
	m.newExtra.Blur()
	switch m.newField {
	case 0:
		m.newName.Focus()
	case 1:
		m.newProfile.Focus()
	case 2:
		m.newCwd.Focus()
	case 3:
		m.newExtra.Focus()
	}
}

var cmdPaletteCommands = []struct {
	name string
	desc string
}{
	{"quit", "Exit dashboard"},
	{"kill", "Kill selected window"},
	{"send", "Send text to selected window"},
	{"attach", "Attach to selected session"},
	{"new", "Create new session"},
	{"filter", "Filter sessions by keyword"},
	{"sessions", "Switch to Sessions view"},
	{"profiles", "Switch to Profiles view"},
	{"activity", "Switch to Activity view"},
	{"history", "Browse all past conversations"},
	{"help", "Show keybindings"},
	{"swap", "Swap active profile (e.g. :swap trax)"},
	{"refresh", "Force refresh all data"},
}

func (m dashModel) updateCommandPalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
		m.cmdSuggestions = nil
		return m, nil
	case "tab":
		if len(m.cmdSuggestions) > 0 {
			m.cmdInput.SetValue(m.cmdSuggestions[m.cmdSugIdx] + " ")
			m.cmdInput.CursorEnd()
			m.cmdSugIdx = (m.cmdSugIdx + 1) % len(m.cmdSuggestions)
		}
		return m, nil
	case "enter":
		cmd := strings.TrimSpace(m.cmdInput.Value())
		m.overlay = overlayNone
		m.cmdSuggestions = nil
		return m.executeCommand(cmd)
	}
	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	m.updateCmdSuggestions()
	return m, cmd
}

func (m *dashModel) updateCmdSuggestions() {
	input := strings.ToLower(strings.TrimSpace(m.cmdInput.Value()))
	if input == "" {
		m.cmdSuggestions = nil
		m.cmdSugIdx = 0
		return
	}
	var matches []string
	for _, c := range cmdPaletteCommands {
		if strings.HasPrefix(c.name, input) {
			matches = append(matches, c.name)
		}
	}
	m.cmdSuggestions = matches
	m.cmdSugIdx = 0
}

func (m dashModel) executeCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return m, nil
	}

	switch parts[0] {
	case "quit", "q":
		return m, tea.Quit
	case "kill":
		filtered := m.filteredEntries()
		if m.cursor < len(filtered) {
			m.overlay = overlayConfirmKill
		}
		return m, nil
	case "send":
		if len(parts) > 1 {
			text := strings.Join(parts[1:], " ")
			filtered := m.filteredEntries()
			if m.cursor < len(filtered) {
				e := filtered[m.cursor]
				_ = mux.SendKeys(e.session, e.window.Index, text)
				m.sendHistory = append(m.sendHistory, text)
				m.histIdx = len(m.sendHistory)
				toastCmd := m.addToast(fmt.Sprintf("Sent to %s:%d", e.session, e.window.Index))
				return m, tea.Batch(toastCmd, dashRefreshCmd())
			}
		} else {
			m.input.Reset()
			m.input.Placeholder = "Type a message to send..."
			m.input.Focus()
			m.overlay = overlaySend
		}
		return m, nil
	case "attach":
		filtered := m.filteredEntries()
		if m.cursor < len(filtered) {
			e := filtered[m.cursor]
			cmd := exec.Command("tmux", "attach-session", "-t", mux.SessionName(e.session))
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return histResumeReturnMsg{}
			})
		}
		return m, nil
	case "new":
		m.newName.Reset()
		m.newProfile.Reset()
		m.newCwd.Reset()
		m.newExtra.Reset()
		m.newField = 0
		m.newName.Focus()
		m.overlay = overlayNewSession
		return m, nil
	case "filter":
		if len(parts) > 1 {
			m.filter.SetValue(strings.Join(parts[1:], " "))
			m.cursor = 0
		}
		m.view = viewSessions
		return m, nil
	case "sessions":
		m.view = viewSessions
		return m, nil
	case "profiles":
		m.view = viewProfiles
		return m, nil
	case "activity":
		m.view = viewActivity
		return m, nil
	case "history":
		if !m.histLoaded {
			m.loadHistory()
		}
		m.view = viewHistory
		return m, nil
	case "help":
		m.view = viewHelp
		return m, nil
	case "swap":
		if len(parts) > 1 {
			m.swapTarget = parts[1]
			m.overlay = overlaySwapConfirm
		} else {
			m.view = viewProfiles
			toastCmd := m.addToast("Select a profile and press Enter to swap")
			return m, toastCmd
		}
		return m, nil
	case "refresh":
		return m, dashRefreshCmd()
	default:
		toastCmd := m.addToast(fmt.Sprintf("Unknown command: %s", parts[0]))
		return m, toastCmd
	}
}

func (m dashModel) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterActive = false
		m.filter.SetValue("")
		m.cursor = 0
		return m, nil
	case "enter":
		m.filterActive = false
		m.filter.Blur()
		m.cursor = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.cursor = 0
	return m, cmd
}

func (m dashModel) filteredEntries() []windowEntry {
	q := strings.ToLower(m.filter.Value())
	if q == "" {
		return m.entries
	}
	var out []windowEntry
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.session), q) ||
			strings.Contains(strings.ToLower(e.profile), q) ||
			strings.Contains(strings.ToLower(e.window.Name), q) ||
			strings.Contains(strings.ToLower(e.window.CWD), q) {
			out = append(out, e)
		}
	}
	return out
}

// ── view ───────────────────────────────────────────────────────────────

func (m dashModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var body string
	switch m.overlay {
	case overlayPeek:
		body = m.renderPeek()
	case overlaySend:
		body = m.renderSend()
	case overlayConfirmKill:
		body = m.renderConfirmKill()
	case overlayConfirmBatchKill:
		body = m.renderBatchKill()
	case overlayNewSession:
		body = m.renderNewSession()
	case overlayAddWindow:
		body = m.renderAddWindow()
	case overlayCommandPalette:
		body = m.renderCommandPalette()
	case overlaySwapConfirm:
		body = m.renderSwapConfirm()
	default:
		switch m.view {
		case viewSessions:
			body = m.renderSessions()
		case viewProfiles:
			body = m.renderProfiles()
		case viewActivity:
			body = m.renderActivity()
		case viewHistory:
			body = m.renderHistory()
		case viewHelp:
			body = m.renderHelp()
		}
	}

	header := m.renderHeader()
	toastBar := m.renderToasts()
	statusBar := m.renderStatusBar()

	if toastBar != "" {
		return header + "\n" + toastBar + "\n" + body + "\n" + statusBar
	}
	return header + "\n" + body + "\n" + statusBar
}

// ── styles ─────────────────────────────────────────────────────────────

var (
	brandPurple  = lipgloss.Color("135")
	brandCyan    = lipgloss.Color("39")
	brandMagenta = lipgloss.Color("205")
	brandGreen   = lipgloss.Color("82")
	brandYellow  = lipgloss.Color("220")
	brandRed     = lipgloss.Color("196")
	brandOrange  = lipgloss.Color("208")
	brandGray    = lipgloss.Color("245")
	brandDim     = lipgloss.Color("241")
	brandWhite   = lipgloss.Color("255")
	brandBgSel   = lipgloss.Color("57")
	brandBgAlt   = lipgloss.Color("234")
	brandColHdr  = lipgloss.Color("248")

	dHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandMagenta)

	dTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandWhite).
			Background(brandPurple).
			Padding(0, 1)

	dTabInactive = lipgloss.NewStyle().
			Foreground(brandGray).
			Padding(0, 1)

	dSessionHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandCyan).
			PaddingLeft(1)

	dSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandWhite).
			Background(brandBgSel)

	dNormal = lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	dStateActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandGreen)

	dStateIdle = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandYellow)

	dStateUnknown = lipgloss.NewStyle().
			Foreground(brandGray)

	dProfile = lipgloss.NewStyle().
			Foreground(brandCyan)

	dCwd = lipgloss.NewStyle().
		Foreground(brandDim)

	dWinName = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	dStatusBar = lipgloss.NewStyle().
			Foreground(brandWhite).
			Background(brandPurple).
			Bold(true)

	dStatusSection = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(brandPurple).
			Padding(0, 1)

	dOverlayBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(brandMagenta).
			Padding(1, 2)

	dOverlayTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandMagenta)

	dHelpKey = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandCyan)

	dHelpDesc = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	dHelpSection = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandMagenta)

	dUsageHigh = lipgloss.NewStyle().Foreground(brandRed)
	dUsageMed  = lipgloss.NewStyle().Foreground(brandYellow)
	dUsageLow  = lipgloss.NewStyle().Foreground(brandGreen)

	dActTime    = lipgloss.NewStyle().Foreground(brandDim)
	dActSession = lipgloss.NewStyle().Foreground(brandCyan).Bold(true)
	dActIdle    = lipgloss.NewStyle().Foreground(brandYellow).Bold(true)
	dActActive  = lipgloss.NewStyle().Foreground(brandGreen).Bold(true)
	dActKilled  = lipgloss.NewStyle().Foreground(brandRed).Bold(true)
	dActCreated = lipgloss.NewStyle().Foreground(brandMagenta).Bold(true)

	dDim    = lipgloss.NewStyle().Foreground(brandDim)
	dBold   = lipgloss.NewStyle().Bold(true).Foreground(brandWhite)
	dAccent = lipgloss.NewStyle().Bold(true).Foreground(brandMagenta)

	dFilterBar = lipgloss.NewStyle().
			Foreground(brandWhite).
			Background(lipgloss.Color("24")).
			Padding(0, 1)

	dToast = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("16")).
		Background(lipgloss.Color("228")).
		Padding(0, 2)

	dRecentChange = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("213"))

	dLiveTag = lipgloss.NewStyle().
			Bold(true).
			Foreground(brandWhite).
			Background(brandRed).
			Padding(0, 1)

	dColHeader = lipgloss.NewStyle().
			Foreground(brandColHdr).
			Bold(true)

	dColSep = lipgloss.NewStyle().
		Foreground(lipgloss.Color("238"))

	dRowAlt = lipgloss.NewStyle().
		Background(brandBgAlt)

	dCmdPalette = lipgloss.NewStyle().
			Foreground(brandWhite).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	dPreview = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Italic(true)
)

// ── render functions ───────────────────────────────────────────────────

func (m dashModel) renderHeader() string {
	logo := dHeaderStyle.Render("⚡ claudeorch")
	version := dDim.Render(" v" + Version)

	tabs := []struct {
		label string
		view  dashView
	}{
		{"1:Sessions", viewSessions},
		{"2:Profiles", viewProfiles},
		{"3:Activity", viewActivity},
		{"4:History", viewHistory},
		{"5:Help", viewHelp},
	}

	var tabStrs []string
	for _, t := range tabs {
		if m.view == t.view {
			tabStrs = append(tabStrs, dTabActive.Render(t.label))
		} else {
			tabStrs = append(tabStrs, dTabInactive.Render(t.label))
		}
	}

	tabBar := strings.Join(tabStrs, " ")

	headerLine := logo + version
	gap := m.width - lipgloss.Width(headerLine) - lipgloss.Width(tabBar) - 2
	if gap < 1 {
		gap = 1
	}
	header := headerLine + strings.Repeat(" ", gap) + tabBar

	separator := lipgloss.NewStyle().
		Foreground(brandPurple).
		Render(strings.Repeat("─", max(m.width, 1)))

	return header + "\n" + separator
}

func (m dashModel) renderToasts() string {
	if len(m.toasts) == 0 {
		return ""
	}
	var parts []string
	for _, t := range m.toasts {
		parts = append(parts, dToast.Render(" "+t.text+" "))
	}
	return "  " + strings.Join(parts, "  ")
}

func (m dashModel) renderStatusBar() string {
	sessionCount := len(m.entries)
	idleCount := 0
	activeCount := 0
	for _, e := range m.entries {
		switch e.state {
		case watch.StateIdle:
			idleCount++
		case watch.StateActive:
			activeCount++
		}
	}

	left := dStatusSection.Render(fmt.Sprintf("Sessions: %d", sessionCount))

	var stateParts []string
	if activeCount > 0 {
		stateParts = append(stateParts, fmt.Sprintf("●%d active", activeCount))
	}
	if idleCount > 0 {
		stateParts = append(stateParts, fmt.Sprintf("○%d idle", idleCount))
	}
	middle := dStatusSection.Render(strings.Join(stateParts, " "))

	uptime := shortDuration(time.Since(m.startTime))
	refresh := "now"
	if !m.lastRefresh.IsZero() {
		refresh = shortDuration(time.Since(m.lastRefresh))
	}
	right := dStatusSection.Render(fmt.Sprintf(": cmd │ ↻ %s │ up %s", refresh, uptime))

	contentWidth := lipgloss.Width(left) + lipgloss.Width(middle) + lipgloss.Width(right)
	fillWidth := m.width - contentWidth
	if fillWidth < 0 {
		fillWidth = 0
	}
	fill := dStatusBar.Render(strings.Repeat(" ", fillWidth))

	return left + middle + fill + right
}

func (m dashModel) renderSessions() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	if m.filterActive || m.filter.Value() != "" {
		if m.filterActive {
			b.WriteString(dFilterBar.Render("/ " + m.filter.View()))
		} else {
			b.WriteString(dFilterBar.Render("/ " + m.filter.Value()))
		}
		b.WriteString("\n")
		available--
	}

	filtered := m.filteredEntries()

	if len(filtered) == 0 {
		b.WriteString("\n")
		if len(m.entries) == 0 {
			b.WriteString(dDim.Render("   No claudeorch tmux sessions running.\n\n"))
			b.WriteString(dDim.Render("   Press ") + dHelpKey.Render("n") + dDim.Render(" to create one, or run:\n"))
			b.WriteString(dAccent.Render("   corch mux start <name> --profile <profile> --cwd <dir>\n"))
		} else {
			b.WriteString(dDim.Render("   No sessions match filter.\n"))
		}
		lines := strings.Count(b.String(), "\n")
		for i := lines; i < available; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}

	// Column header
	hdr := fmt.Sprintf("  %-2s %-9s  %-10s %-14s %-25s %s",
		"", "STATE", "SESSION", "WINDOW", "PATH", "OUTPUT")
	b.WriteString(dColHeader.Render(hdr) + "\n")
	b.WriteString("  " + dColSep.Render(strings.Repeat("─", min(m.width-4, 120))) + "\n")
	available -= 2

	lineCount := 0
	rowIdx := 0

	for i, e := range filtered {
		if lineCount >= available-1 {
			break
		}

		// State indicator with recent-change marker
		recentlyChanged := !e.stateChanged.IsZero() && time.Since(e.stateChanged) < 30*time.Second
		changeMarker := " "
		if recentlyChanged {
			changeMarker = dRecentChange.Render("⚡")
		}

		var stateIcon string
		switch e.state {
		case watch.StateActive:
			stateIcon = dStateActive.Render("● ACTIVE")
		case watch.StateIdle:
			stateIcon = dStateIdle.Render("◉ IDLE  ")
		default:
			stateIcon = dStateUnknown.Render("○ ····· ")
		}

		checkbox := "  "
		if e.selected {
			checkbox = lipgloss.NewStyle().Foreground(brandMagenta).Bold(true).Render("✓ ")
		}

		sessionName := dProfile.Render(fmt.Sprintf("%-10s", e.session))
		winInfo := dWinName.Render(fmt.Sprintf("%-14s", e.window.Name))

		attachTag := "  "
		if e.attached > 0 {
			attachTag = lipgloss.NewStyle().Bold(true).Foreground(brandGreen).Render("⊞ ")
		}

		// Output preview
		previewWidth := m.width - 70
		preview := ""
		if e.lastLine != "" && previewWidth > 10 {
			preview = dPreview.Render(" " + truncate(e.lastLine, previewWidth))
		}

		cwdInfo := dCwd.Render(truncatePath(e.window.CWD, 25))

		line := fmt.Sprintf("  %s%s%s %s%s %s %s%s", checkbox, stateIcon, changeMarker, attachTag, sessionName, winInfo, cwdInfo, preview)

		if i == m.cursor {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dSelected.Render(padded))
		} else if rowIdx%2 == 1 {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dRowAlt.Render(padded))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
		lineCount++
		rowIdx++
	}

	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

func (m dashModel) renderProfiles() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	b.WriteString("\n")
	available--

	if len(m.profiles) == 0 {
		b.WriteString(dDim.Render("   No profiles found.\n"))
		for i := 2; i < available; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}

	// Summary bar at top
	totalSessions := 0
	totalProfiles := len(m.profiles)
	for _, p := range m.profiles {
		totalSessions += p.sessionCount
	}
	summaryLine := dAccent.Render(fmt.Sprintf("  %d profiles", totalProfiles))
	if totalSessions > 0 {
		summaryLine += dDim.Render(" │ ") + dStateActive.Render(fmt.Sprintf("%d sessions", totalSessions))
	}
	b.WriteString(summaryLine + "\n")
	available--

	hdr := fmt.Sprintf("  %-3s %-18s %-28s %-10s %-10s %-6s %-8s",
		"", "NAME", "EMAIL", "SOURCE", "LOCATION", "SESS", "USAGE")
	b.WriteString(dColHeader.Render(hdr) + "\n")
	b.WriteString("  " + dColSep.Render(strings.Repeat("─", min(m.width-4, 110))) + "\n")
	lineCount := 4

	for i, p := range m.profiles {
		if lineCount >= available {
			break
		}

		activeMarker := "  "
		if p.active {
			activeMarker = lipgloss.NewStyle().Foreground(brandGreen).Bold(true).Render("▸ ")
		}

		nameStyle := dNormal
		if p.active {
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(brandCyan)
		}

		sourceStyle := dDim
		if p.source == "api-key" {
			sourceStyle = lipgloss.NewStyle().Foreground(brandOrange)
		}

		locStyle := dDim
		switch p.location {
		case "live":
			locStyle = lipgloss.NewStyle().Foreground(brandGreen)
		case "isolated":
			locStyle = lipgloss.NewStyle().Foreground(brandYellow)
		}

		sessStr := dDim.Render("  -")
		if p.sessionCount > 0 {
			sessStr = lipgloss.NewStyle().Foreground(brandGreen).Bold(true).Render(fmt.Sprintf("  %d", p.sessionCount))
		}

		usageBar := renderMiniBar(p.usage5h)

		line := fmt.Sprintf("%s%-18s %-28s %-10s %-10s %-6s %s",
			activeMarker,
			nameStyle.Render(p.name),
			dDim.Render(truncate(p.email, 26)),
			sourceStyle.Render(p.source),
			locStyle.Render(p.location),
			sessStr,
			usageBar)

		if i == m.profCursor {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dSelected.Render(padded))
		} else if i%2 == 1 {
			padded := "  " + line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-3, 0))
			b.WriteString(dRowAlt.Render(padded))
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
		lineCount++

		if i == m.profCursor && (p.usage5h > 0 || p.usage7d > 0) {
			detail := fmt.Sprintf("       5H: %s %3d%%",
				renderUsageBar(p.usage5h, 20), int(p.usage5h*100+0.5))
			if p.reset5h != "" {
				detail += fmt.Sprintf("  resets %s", p.reset5h)
			}
			b.WriteString(dDim.Render(detail) + "\n")
			lineCount++

			detail7d := fmt.Sprintf("       7D: %s %3d%%",
				renderUsageBar(p.usage7d, 20), int(p.usage7d*100+0.5))
			if p.reset7d != "" {
				detail7d += fmt.Sprintf("  resets %s", p.reset7d)
			}
			b.WriteString(dDim.Render(detail7d) + "\n")
			lineCount++
		}
	}

	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

func (m dashModel) renderActivity() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	// Column header
	actHdr := fmt.Sprintf("  %-10s %-22s  %-12s %s", "TIME", "SESSION", "EVENT", "PROFILE")
	b.WriteString(dColHeader.Render(actHdr) + "\n")
	b.WriteString("  " + dColSep.Render(strings.Repeat("─", min(m.width-4, 80))) + "\n")
	available -= 2

	if m.actFilterActive || m.actFilter.Value() != "" {
		if m.actFilterActive {
			b.WriteString(dFilterBar.Render("/ " + m.actFilter.View()))
		} else {
			b.WriteString(dFilterBar.Render("/ " + m.actFilter.Value()))
		}
		b.WriteString("\n")
		available--
	}

	filtered := m.filteredActivities()

	if len(filtered) == 0 {
		if len(m.activities) == 0 {
			b.WriteString(dDim.Render("   No activity yet. State changes will appear here.\n"))
			b.WriteString(dDim.Render("   The log populates as sessions transition between ACTIVE and IDLE.\n"))
		} else {
			b.WriteString(dDim.Render("   No activity matches filter.\n"))
		}
		lineCount := strings.Count(b.String(), "\n")
		for i := lineCount; i < available; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}

	// Ensure cursor is in bounds
	actCursor := m.actCursor
	if actCursor >= len(filtered) {
		actCursor = len(filtered) - 1
	}

	// Calculate visible window around cursor
	visibleCount := available
	startIdx := actCursor - visibleCount/2
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + visibleCount
	if endIdx > len(filtered) {
		endIdx = len(filtered)
		startIdx = endIdx - visibleCount
		if startIdx < 0 {
			startIdx = 0
		}
	}

	// Scroll indicator
	if startIdx > 0 {
		b.WriteString(dDim.Render(fmt.Sprintf("  ▲ %d more above", startIdx)) + "\n")
		available--
	}

	lineCount := 0
	for i := startIdx; i < endIdx && lineCount < available-1; i++ {
		a := filtered[i]
		ts := dActTime.Render(a.timestamp.Format("15:04:05"))
		sess := dActSession.Render(fmt.Sprintf("%s:%d", a.session, a.window))

		var eventStr string
		switch a.event {
		case "KILLED":
			eventStr = dActKilled.Render("✕ KILLED")
		case "CREATED":
			eventStr = dActCreated.Render("✦ CREATED")
		default:
			if a.stateTo == watch.StateIdle {
				eventStr = dActIdle.Render("◉ → IDLE")
			} else if a.stateTo == watch.StateActive {
				eventStr = dActActive.Render("● → ACTIVE")
			} else {
				eventStr = dDim.Render(a.event)
			}
		}

		profileTag := dProfile.Render(fmt.Sprintf("[%s]", a.profile))
		line := fmt.Sprintf("  %s  %-22s  %s  %s", ts, sess, eventStr, profileTag)

		rowNum := i - startIdx
		if i == actCursor {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dSelected.Render(padded))
		} else if rowNum%2 == 1 {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dRowAlt.Render(padded))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
		lineCount++
	}

	// Scroll indicator at bottom
	if endIdx < len(filtered) {
		b.WriteString(dDim.Render(fmt.Sprintf("  ▼ %d more below", len(filtered)-endIdx)) + "\n")
		lineCount++
	}

	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

func (m dashModel) renderHelp() string {
	var b strings.Builder

	sections := []struct {
		title string
		keys  [][2]string
	}{
		{
			"Navigation",
			[][2]string{
				{"↑/↓  j/k", "Move cursor up/down"},
				{"g / G", "Jump to top / bottom"},
				{"1-5", "Switch views (Sessions, Profiles, Activity, History, Help)"},
				{"←/→  Tab", "Cycle through tabs"},
				{"r", "Force refresh"},
				{":", "Command palette (quit, kill, send, attach, new, filter)"},
				{"q  ctrl+c", "Quit dashboard"},
			},
		},
		{
			"Sessions View",
			[][2]string{
				{"Enter  p", "Peek — live-tail window output (auto-refreshes)"},
				{"s", "Send text to selected window"},
				{"b", "Broadcast text to all windows in session"},
				{"a", "Attach in new iTerm2 tab (stays in dash)"},
				{"A", "Attach inline (Ctrl-a d returns to dash)"},
				{"d", "Kill selected window (with confirmation)"},
				{"D", "Batch kill all selected windows"},
				{"n", "Create new session"},
				{"w", "Add window to current session"},
				{"Space", "Toggle selection (for batch ops)"},
				{"/  f", "Filter sessions by name/profile/path"},
				{"Esc", "Clear selections / clear filter"},
			},
		},
		{
			"Peek Mode (Live Tail)",
			[][2]string{
				{"↑/↓", "Scroll output"},
				{"s", "Inline send — type and send without leaving peek"},
				{"r", "Force refresh output"},
				{"Esc  q", "Back to list"},
			},
		},
		{
			"Profiles View",
			[][2]string{
				{"↑/↓  j/k", "Move cursor"},
				{"Enter", "Swap to selected profile"},
				{"n", "New session with this profile"},
				{"g / G", "Jump to top / bottom"},
			},
		},
		{
			"Activity View",
			[][2]string{
				{"↑/↓  j/k", "Scroll through events"},
				{"g / G", "Jump to first / last event"},
				{"/  f", "Filter activity log"},
				{"c", "Clear activity log"},
			},
		},
		{
			"History View",
			[][2]string{
				{"Enter  i", "Show session details"},
				{"a", "Attach in new iTerm2 tab (stays in dash)"},
				{"A", "Attach inline (Ctrl-a d returns to dash)"},
				{"O", "Open inline (dash suspends, /exit returns)"},
				{"c", "Clone session (branch off in new direction)"},
				{"R", "Reload history"},
			},
		},
	}

	available := m.height - 7
	if len(m.toasts) > 0 {
		available--
	}
	if available < 5 {
		available = 5
	}
	b.WriteString("\n")
	lineCount := 1

	for _, sec := range sections {
		if lineCount >= available {
			break
		}
		b.WriteString("  " + dHelpSection.Render(sec.title) + "\n")
		lineCount++
		for _, kv := range sec.keys {
			if lineCount >= available {
				break
			}
			b.WriteString(fmt.Sprintf("    %s  %s\n",
				dHelpKey.Render(fmt.Sprintf("%-12s", kv[0])),
				dHelpDesc.Render(kv[1])))
			lineCount++
		}
		b.WriteString("\n")
		lineCount++
	}

	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

// ── overlay renders ────────────────────────────────────────────────────

func (m dashModel) renderPeek() string {
	var b strings.Builder

	filtered := m.filteredEntries()
	if m.cursor < len(filtered) {
		e := filtered[m.cursor]
		var stateTag string
		switch e.state {
		case watch.StateActive:
			stateTag = dStateActive.Render(" ACTIVE ")
		case watch.StateIdle:
			stateTag = dStateIdle.Render(" IDLE ")
		}
		title := dOverlayTitle.Render(fmt.Sprintf("  ⚡ %s:%d ", e.session, e.window.Index))
		liveTag := dLiveTag.Render(" LIVE ")
		b.WriteString(title + stateTag + " " + dProfile.Render("["+e.profile+"]") + "  " + liveTag + "\n")
	}

	sep := lipgloss.NewStyle().Foreground(brandMagenta).
		Render(strings.Repeat("─", max(m.width, 1)))
	b.WriteString(sep + "\n")

	b.WriteString(m.peek.View())
	b.WriteString("\n")

	if m.peekSend {
		sendBar := lipgloss.NewStyle().
			Foreground(brandWhite).
			Background(lipgloss.Color("24")).
			Padding(0, 1).
			Render("Send: " + m.input.View())
		b.WriteString(sendBar + "\n")
	} else {
		scrollInfo := dDim.Render(fmt.Sprintf(" %d%% ", int(m.peek.ScrollPercent()*100)))
		footer := dDim.Render("  ↑/↓ scroll • s send inline • r refresh • esc back") + "  " + scrollInfo
		b.WriteString(footer)
	}

	return b.String()
}

func (m dashModel) renderSend() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	filtered := m.filteredEntries()
	target := "?"
	if m.cursor < len(filtered) {
		e := filtered[m.cursor]
		target = fmt.Sprintf("%s:%d", e.session, e.window.Index)
	}

	b.WriteString("\n")
	boxWidth := min(m.width-8, 80)
	title := dOverlayTitle.Render("  ⚡ Send to " + target)
	b.WriteString(title + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(brandMagenta).
		Render("  " + strings.Repeat("─", boxWidth)) + "\n\n")

	b.WriteString("    " + m.input.View() + "\n\n")

	if len(m.sendHistory) > 0 {
		b.WriteString(dDim.Render("    ↑/↓ for history") + "\n")
	}

	b.WriteString("\n")
	b.WriteString(dDim.Render("    enter send • esc cancel") + "\n")

	lineCount := 8
	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

func (m dashModel) renderConfirmKill() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	filtered := m.filteredEntries()
	target := "?"
	if m.cursor < len(filtered) {
		e := filtered[m.cursor]
		target = fmt.Sprintf("%s:%d (%s)", e.session, e.window.Index, e.window.Name)
	}

	b.WriteString("\n\n\n")

	box := dOverlayBorder.Width(min(m.width-10, 60)).Render(
		dOverlayTitle.Render("  Kill Window?") + "\n\n" +
			"  " + dBold.Render(target) + "\n\n" +
			"  " + dDim.Render("This will terminate the Claude instance.") + "\n\n" +
			"  " + dHelpKey.Render("y") + dDim.Render(" confirm  ") +
			dHelpKey.Render("n") + dDim.Render(" cancel"))

	lines := strings.Split(box, "\n")
	for _, line := range lines {
		pad := (m.width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}

	lineCount := strings.Count(b.String(), "\n")
	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

func (m dashModel) renderNewSession() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	b.WriteString("\n\n")

	fieldLabel := func(idx int, label string) string {
		if idx == m.newField {
			return dAccent.Render("▸ " + label)
		}
		return dDim.Render("  " + label)
	}

	hint := "  tab/↓ next field • enter submit • esc cancel"
	if m.newProfile.Value() != "" && m.newField == 0 {
		hint = "  enter to launch • tab to edit defaults • esc cancel"
	}

	content := dOverlayTitle.Render("  ⚡ New Session") + "\n\n" +
		fieldLabel(0, "Name:    ") + " " + m.newName.View() + "\n\n" +
		fieldLabel(1, "Profile: ") + " " + m.newProfile.View() + "\n\n" +
		fieldLabel(2, "CWD:     ") + " " + m.newCwd.View() + "\n\n" +
		fieldLabel(3, "Args:    ") + " " + m.newExtra.View() + "\n\n" +
		dDim.Render(hint)

	box := dOverlayBorder.Width(min(m.width-10, 70)).Render(content)

	lines := strings.Split(box, "\n")
	for _, line := range lines {
		pad := (m.width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}

	lineCount := strings.Count(b.String(), "\n")
	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}

	return b.String()
}

func (m dashModel) renderCommandPalette() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	// Render current view behind the palette
	switch m.view {
	case viewSessions:
		b.WriteString(m.renderSessions())
	case viewProfiles:
		b.WriteString(m.renderProfiles())
	case viewActivity:
		b.WriteString(m.renderActivity())
	case viewHistory:
		b.WriteString(m.renderHistory())
	case viewHelp:
		b.WriteString(m.renderHelp())
	}

	_ = available

	result := b.String()
	lines := strings.Split(result, "\n")

	// Build suggestion hint
	sugHint := ""
	if len(m.cmdSuggestions) > 0 {
		var parts []string
		for i, s := range m.cmdSuggestions {
			if i == m.cmdSugIdx {
				parts = append(parts, dAccent.Render(s))
			} else {
				parts = append(parts, dDim.Render(s))
			}
			if i >= 5 {
				parts = append(parts, dDim.Render("..."))
				break
			}
		}
		sugHint = "  " + strings.Join(parts, dDim.Render(" │ "))
	} else if m.cmdInput.Value() == "" {
		var hints []string
		for i, c := range cmdPaletteCommands {
			if i >= 6 {
				hints = append(hints, dDim.Render("..."))
				break
			}
			hints = append(hints, dDim.Render(c.name))
		}
		sugHint = "  " + strings.Join(hints, dDim.Render(" │ "))
	}

	// Replace last two lines with suggestions + command bar
	if len(lines) > 2 && sugHint != "" {
		lines[len(lines)-3] = dCmdPalette.Width(m.width).Render(sugHint)
	}
	if len(lines) > 1 {
		lines[len(lines)-2] = dCmdPalette.Width(m.width).Render(":" + m.cmdInput.View() + dDim.Render("  tab complete"))
	}
	return strings.Join(lines, "\n")
}

func (m dashModel) renderBatchKill() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	selected := m.selectedEntries()
	b.WriteString("\n\n\n")

	var windowList strings.Builder
	for _, e := range selected {
		windowList.WriteString(fmt.Sprintf("    %s  %s:%d\n",
			dActKilled.Render("✕"),
			dBold.Render(e.session),
			e.window.Index))
	}

	box := dOverlayBorder.Width(min(m.width-10, 60)).Render(
		dOverlayTitle.Render("  Kill Selected Windows?") + "\n\n" +
			windowList.String() + "\n" +
			dDim.Render(fmt.Sprintf("  %d windows will be terminated.", len(selected))) + "\n\n" +
			"  " + dHelpKey.Render("y") + dDim.Render(" confirm  ") +
			dHelpKey.Render("n") + dDim.Render(" cancel"))

	lines := strings.Split(box, "\n")
	for _, line := range lines {
		pad := (m.width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}

	lineCount := strings.Count(b.String(), "\n")
	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}
	return b.String()
}

func (m dashModel) renderAddWindow() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	filtered := m.filteredEntries()
	target := "?"
	if m.cursor < len(filtered) {
		target = filtered[m.cursor].session
	}

	b.WriteString("\n\n")
	content := dOverlayTitle.Render("  ⚡ Add Window to "+target) + "\n\n" +
		dAccent.Render("  CWD: ") + " " + m.addWinCwd.View() + "\n\n" +
		dDim.Render("  enter create • esc cancel")

	box := dOverlayBorder.Width(min(m.width-10, 70)).Render(content)
	lines := strings.Split(box, "\n")
	for _, line := range lines {
		pad := (m.width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}

	lineCount := strings.Count(b.String(), "\n")
	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}
	return b.String()
}

func (m dashModel) renderSwapConfirm() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	b.WriteString("\n\n\n")

	box := dOverlayBorder.Width(min(m.width-10, 60)).Render(
		dOverlayTitle.Render("  Swap Active Profile?") + "\n\n" +
			"  Switch to: " + dBold.Render(m.swapTarget) + "\n\n" +
			dDim.Render("  This will change the active Claude credentials.") + "\n\n" +
			"  " + dHelpKey.Render("y") + dDim.Render(" confirm  ") +
			dHelpKey.Render("n") + dDim.Render(" cancel"))

	lines := strings.Split(box, "\n")
	for _, line := range lines {
		pad := (m.width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}

	lineCount := strings.Count(b.String(), "\n")
	for lineCount < available {
		b.WriteString("\n")
		lineCount++
	}
	return b.String()
}

// ── helpers ────────────────────────────────────────────────────────────

func renderUsageBar(pct float64, width int) string {
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled

	var style lipgloss.Style
	switch {
	case pct >= 0.8:
		style = dUsageHigh
	case pct >= 0.5:
		style = dUsageMed
	default:
		style = dUsageLow
	}

	bar := style.Render(strings.Repeat("█", filled)) +
		dDim.Render(strings.Repeat("░", empty))
	return bar
}

func renderMiniBar(pct float64) string {
	if pct <= 0 {
		return dDim.Render("·····")
	}
	return renderUsageBar(pct, 8) + dDim.Render(fmt.Sprintf(" %d%%", int(pct*100+0.5)))
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 40
	}
	if len(s) <= maxLen {
		return s
	}
	return "…" + s[len(s)-maxLen+1:]
}

func truncatePath(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 30
	}
	home, _ := paths.ClaudeConfigHome()
	homeDir := ""
	if idx := strings.LastIndex(home, "/.claude"); idx >= 0 {
		homeDir = home[:idx]
	}
	if homeDir != "" {
		s = strings.Replace(s, homeDir, "~", 1)
	}
	if len(s) <= maxLen {
		return s
	}
	return "…" + s[len(s)-maxLen+1:]
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── history view ──────────────────────────────────────────────────────

func (m *dashModel) loadHistory() {
	home, _ := os.UserHomeDir()
	claudeDir := home + "/.claude"
	isolateDir := home + "/.claudeorch/isolate"
	convos, _ := session.ScanConversations(claudeDir, isolateDir)
	// Scan first prompts for top 50 only
	limit := 50
	if len(convos) < limit {
		limit = len(convos)
	}
	for i := 0; i < limit; i++ {
		session.ScanFirstPrompt(&convos[i])
	}
	m.history = convos
	m.histLoaded = true
	m.histCursor = 0
}

func (m dashModel) updateHistory(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.histCursor > 0 {
			m.histCursor--
		}
	case "down", "j":
		if m.histCursor < len(m.history)-1 {
			m.histCursor++
		}
	case "g":
		m.histCursor = 0
	case "G":
		if len(m.history) > 0 {
			m.histCursor = len(m.history) - 1
		}
	case "R":
		m.loadHistory()
		toastCmd := m.addToast("History reloaded")
		return m, toastCmd
	case "a":
		if m.histCursor < len(m.history) {
			c := m.history[m.histCursor]
			sessionName := "resume"
			resumeCmd := fmt.Sprintf("cd %s && claude --resume %s; exec $SHELL",
				shellQuoteDash(c.CWD), c.SessionID)
			if !mux.SessionExists(sessionName) {
				_ = mux.CreateSession(sessionName, c.Profile, "resume-1", resumeCmd)
			} else {
				winCount, _ := mux.WindowCount(sessionName)
				winName := fmt.Sprintf("resume-%d", winCount+1)
				_ = mux.AddWindow(sessionName, winName, resumeCmd)
			}
			tmuxTarget := mux.SessionName(sessionName)
			if isIterm() {
				return m, openInItermTab("tmux attach-session -t " + tmuxTarget)
			}
			attachCmd := exec.Command("tmux", "attach-session", "-t", tmuxTarget)
			return m, tea.ExecProcess(attachCmd, func(err error) tea.Msg {
				return histResumeReturnMsg{}
			})
		}
	case "A":
		if m.histCursor < len(m.history) {
			c := m.history[m.histCursor]
			sessionName := "resume"
			resumeCmd := fmt.Sprintf("cd %s && claude --resume %s; exec $SHELL",
				shellQuoteDash(c.CWD), c.SessionID)
			if !mux.SessionExists(sessionName) {
				_ = mux.CreateSession(sessionName, c.Profile, "resume-1", resumeCmd)
			} else {
				winCount, _ := mux.WindowCount(sessionName)
				winName := fmt.Sprintf("resume-%d", winCount+1)
				_ = mux.AddWindow(sessionName, winName, resumeCmd)
			}
			attachCmd := exec.Command("tmux", "attach-session", "-t", mux.SessionName(sessionName))
			return m, tea.ExecProcess(attachCmd, func(err error) tea.Msg {
				return histResumeReturnMsg{}
			})
		}
	case "O":
		if m.histCursor < len(m.history) {
			c := m.history[m.histCursor]
			cmd := exec.Command("claude", "--resume", c.SessionID)
			cmd.Dir = c.CWD
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return histResumeReturnMsg{}
			})
		}
	case "enter", "i":
		if m.histCursor < len(m.history) {
			c := m.history[m.histCursor]
			session.ScanFirstPrompt(&m.history[m.histCursor])
			detail := fmt.Sprintf("Session: %s\nProfile: %s\nCWD: %s\nSize: %s\nModified: %s\n\nFirst prompt: %s\n\nResume:\n  cd %s && claude --resume %s",
				c.SessionID, c.Profile, c.CWD,
				histHumanSize(c.Size), c.ModTime.Format("2006-01-02 15:04"),
				c.FirstPrompt, c.CWD, c.SessionID)
			m.peek.SetContent(detail)
			m.peek.GotoTop()
			m.peekTarget = ""
			m.overlay = overlayPeek
		}
	case "c":
		if m.histCursor < len(m.history) {
			orig := m.history[m.histCursor]
			clone, err := session.CloneConversation(&orig)
			if err != nil {
				toastCmd := m.addToast("Clone failed: " + err.Error())
				return m, toastCmd
			}
			m.history = append([]session.Conversation{*clone}, m.history...)
			m.histCursor = 0
			shortID := clone.SessionID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			toastCmd := m.addToast("Cloned → " + shortID + " (a to resume)")
			return m, toastCmd
		}
	}
	return m, nil
}

func (m dashModel) renderHistory() string {
	var b strings.Builder
	available := m.height - 5
	if len(m.toasts) > 0 {
		available--
	}

	// Context bar
	b.WriteString(dDim.Render(fmt.Sprintf("  %d conversations", len(m.history))))
	b.WriteString("  ")
	b.WriteString(dDim.Render("a new tab • A inline attach • O inline claude • c clone • enter info • R reload"))
	b.WriteString("\n")
	available--

	if len(m.history) == 0 {
		b.WriteString(dDim.Render("  No conversations found."))
		b.WriteString("\n")
		available--
		for available > 0 {
			b.WriteString("\n")
			available--
		}
		return b.String()
	}

	// Column header
	hdr := fmt.Sprintf("  %-2s %-8s %-6s %5s %-25s %-8s %s",
		"", "PROFILE", "AGE", "SIZE", "PATH", "ID", "PROMPT")
	b.WriteString(dColHeader.Render(hdr) + "\n")
	b.WriteString("  " + dColSep.Render(strings.Repeat("─", min(m.width-4, 120))) + "\n")
	available -= 2

	// Scrolling window
	visibleRows := available
	if visibleRows < 1 {
		visibleRows = 1
	}
	start := 0
	if m.histCursor >= visibleRows {
		start = m.histCursor - visibleRows + 1
	}
	end := start + visibleRows
	if end > len(m.history) {
		end = len(m.history)
	}

	profileColors := map[string]lipgloss.Style{
		"(live)":      lipgloss.NewStyle().Foreground(brandGreen),
		"bala":        lipgloss.NewStyle().Foreground(brandCyan),
		"trax":        lipgloss.NewStyle().Foreground(brandMagenta),
		"form":        lipgloss.NewStyle().Foreground(brandYellow),
		"claude-corp": lipgloss.NewStyle().Foreground(brandOrange),
	}

	rowIdx := 0
	for i := start; i < end; i++ {
		c := m.history[i]
		cursor := "  "
		if i == m.histCursor {
			cursor = dAccent.Render("▸ ")
		}

		profStyle, ok := profileColors[c.Profile]
		if !ok {
			profStyle = lipgloss.NewStyle().Foreground(brandGray)
		}

		age := histShortAge(c.ModTime)
		sizeStr := histHumanSize(c.Size)

		prompt := c.FirstPrompt
		if prompt == "" {
			prompt = "(not scanned)"
		}

		cwdShort := c.CWD
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(cwdShort, home) {
			cwdShort = "~" + cwdShort[len(home):]
		}
		if len(cwdShort) > 25 {
			cwdShort = "..." + cwdShort[len(cwdShort)-22:]
		}

		shortID := c.SessionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		maxPrompt := m.width - 70
		if maxPrompt < 20 {
			maxPrompt = 20
		}
		if len(prompt) > maxPrompt {
			prompt = prompt[:maxPrompt] + "..."
		}

		line := fmt.Sprintf("%s%s %-6s %5s %-25s %s  %s",
			cursor,
			profStyle.Render(fmt.Sprintf("%-8s", c.Profile)),
			dDim.Render(age),
			dDim.Render(sizeStr),
			dDim.Render(cwdShort),
			dDim.Render(shortID),
			prompt,
		)

		if i == m.histCursor {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dSelected.Render(padded))
		} else if rowIdx%2 == 1 {
			padded := line + strings.Repeat(" ", max(m.width-lipgloss.Width(line)-1, 0))
			b.WriteString(dRowAlt.Render(padded))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
		available--
		rowIdx++
	}

	for available > 0 {
		b.WriteString("\n")
		available--
	}

	return b.String()
}

func histShortAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
}

func shellQuoteDash(s string) string {
	if strings.ContainsAny(s, " '\"\\$!") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}

type itermTabResultMsg struct{ err error }

func openInItermTab(shellCmd string) tea.Cmd {
	return func() tea.Msg {
		if tmuxPath, err := exec.LookPath("tmux"); err == nil {
			shellCmd = strings.Replace(shellCmd, "tmux ", tmuxPath+" ", 1)
		}
		script := fmt.Sprintf(`tell application "iTerm2"
	tell current window
		create tab with default profile command %q
	end tell
end tell`, shellCmd)
		err := exec.Command("osascript", "-e", script).Run()
		return itermTabResultMsg{err}
	}
}

func isIterm() bool {
	return os.Getenv("TERM_PROGRAM") == "iTerm.app"
}

func histHumanSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.0fK", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(bytes)/(1024*1024))
	}
}
