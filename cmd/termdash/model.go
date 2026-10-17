package termdash

import (
	"strings"
	"time"

	"github.com/calypr/funnel/tes"
	tea "github.com/charmbracelet/bubbletea"
)

const refreshInterval = time.Second

type mode int

const (
	modeList mode = iota
	modeFilter
	modeSort
	modeHelp
	modeExpanded
)

// tickMsg drives the auto-refresh loop.
type tickMsg time.Time

// fetchedListMsg carries a refreshed task list.
type fetchedListMsg struct {
	tasks []*tes.Task
	err   error
}

// fetchedTaskMsg carries a fully-detailed task for the expanded view.
type fetchedTaskMsg struct {
	task *tes.Task
	err  error
}

type dashboard struct {
	source *TaskSource

	mode   mode
	width  int
	height int

	tasks      []*tes.Task // sorted/filtered current page
	selected   string      // selected task ID
	offset     int         // first visible row
	err        string
	filter     string
	filterBuf  string // in-progress filter input
	sortField  string
	reversed   bool
	allTasks   bool
	showHeader bool

	// expanded view
	expandedID   string
	expandedJSON []string
	expOffset    int

	// sort menu
	sortCursor int
}

func newDashboard(server string) (*dashboard, error) {
	src, err := NewTaskSource(server, 100)
	if err != nil {
		return nil, err
	}
	return &dashboard{
		source:     src,
		mode:       modeList,
		sortField:  "id",
		showHeader: true,
	}, nil
}

func (d *dashboard) Init() tea.Cmd {
	return tea.Batch(fetchList(d, false, false), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func fetchList(d *dashboard, prev, next bool) tea.Cmd {
	src := d.source
	sortField, reversed, allTasks, filter := d.sortField, d.reversed, d.allTasks, d.filter
	return func() tea.Msg {
		tasks, err := src.List(prev, next, sortField, reversed, allTasks, filter)
		return fetchedListMsg{tasks: tasks, err: err}
	}
}

func fetchTask(src *TaskSource, id string) tea.Cmd {
	return func() tea.Msg {
		t, err := src.Get(id)
		return fetchedTaskMsg{task: t, err: err}
	}
}

func (d *dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height = msg.Width, msg.Height
		d.clampOffset()
		return d, nil

	case tickMsg:
		var cmd tea.Cmd
		if d.mode == modeList {
			cmd = fetchList(d, false, false)
		}
		return d, tea.Batch(cmd, tick())

	case fetchedListMsg:
		if msg.err != nil {
			d.err = msg.err.Error()
		} else {
			d.err = ""
			d.tasks = msg.tasks
			if d.selected == "" || d.findSelected() < 0 {
				d.offset = 0
				d.selected = ""
				if len(d.tasks) > 0 {
					d.selected = d.tasks[0].Id
				}
			}
			d.clampOffset()
		}
		return d, nil

	case fetchedTaskMsg:
		if msg.err != nil {
			d.err = msg.err.Error()
			return d, nil
		}
		d.expandedID = msg.task.Id
		d.expandedJSON = strings.Split(taskJSON(msg.task), "\n")
		d.expOffset = 0
		d.mode = modeExpanded
		return d, nil

	case tea.KeyMsg:
		switch d.mode {
		case modeFilter:
			return d.updateFilter(msg)
		case modeSort:
			return d.updateSort(msg)
		case modeHelp:
			d.mode = modeList
			return d, nil
		case modeExpanded:
			return d.updateExpanded(msg)
		default:
			return d.updateList(msg)
		}
	}
	return d, nil
}

func (d *dashboard) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		d.moveCursor(-1)
	case "down", "j":
		d.moveCursor(1)
	case "pgup":
		d.moveCursor(-d.maxRows())
	case "pgdown":
		d.moveCursor(d.maxRows())
	case "home", "g":
		d.offset = 0
		d.setSelectedIdx(0)
	case "end", "G":
		d.setSelectedIdx(len(d.tasks) - 1)
		d.clampOffset()
	case "enter":
		if t := d.selectedTask(); t != nil {
			return d, fetchTask(d.source, t.Id)
		}
	case "n":
		if d.source.NextPageToken() != "" {
			return d, fetchList(d, false, true)
		}
	case "p":
		if d.source.PreviousPageToken() != "" {
			return d, fetchList(d, true, false)
		}
	case "f":
		d.mode = modeFilter
		d.filterBuf = d.filter
	case "a":
		d.allTasks = !d.allTasks
		return d, fetchList(d, false, false)
	case "s":
		d.mode = modeSort
		d.sortCursor = indexOf(SortFields(), d.sortField)
	case "r":
		d.reversed = !d.reversed
		return d, fetchList(d, false, false)
	case "H":
		d.showHeader = !d.showHeader
	case "h", "?":
		d.mode = modeHelp
	case "q", "esc", "ctrl+c":
		return d, tea.Quit
	}
	return d, nil
}

func (d *dashboard) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		d.filter = d.filterBuf
		d.mode = modeList
		d.selected = ""
		return d, fetchList(d, false, false)
	case "esc":
		// clear the filter entirely, matching old behavior
		d.filter = ""
		d.filterBuf = ""
		d.mode = modeList
		d.selected = ""
		return d, fetchList(d, false, false)
	case "backspace":
		if len(d.filterBuf) > 0 {
			d.filterBuf = d.filterBuf[:len(d.filterBuf)-1]
		}
	case "ctrl+c":
		return d, tea.Quit
	case "up", "down", "left", "right":
		// ignore navigation keys while typing
	default:
		if len(msg.Runes) > 0 {
			d.filterBuf += string(msg.Runes)
		}
	}
	return d, nil
}

func (d *dashboard) updateSort(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fields := SortFields()
	switch msg.String() {
	case "up", "k":
		if d.sortCursor > 0 {
			d.sortCursor--
		}
	case "down", "j":
		if d.sortCursor < len(fields)-1 {
			d.sortCursor++
		}
	case "enter":
		d.sortField = fields[d.sortCursor]
		d.mode = modeList
		return d, fetchList(d, false, false)
	case "esc", "q", "ctrl+c":
		d.mode = modeList
	}
	return d, nil
}

func (d *dashboard) updateExpanded(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	maxLines := d.expMaxLines()
	switch msg.String() {
	case "up", "k":
		if d.expOffset > 0 {
			d.expOffset--
		}
	case "down", "j":
		if maxLines > 0 && d.expOffset < len(d.expandedJSON)-maxLines {
			d.expOffset++
		}
	case "pgup":
		d.expOffset = maxInt(0, d.expOffset-maxLines)
	case "pgdown":
		if maxLines > 0 {
			d.expOffset = minInt(len(d.expandedJSON)-maxLines, d.expOffset+maxLines)
		}
	case "esc", "q", "h":
		d.mode = modeList
	case "ctrl+c":
		return d, tea.Quit
	}
	return d, nil
}

// cursor and window helpers

func (d *dashboard) maxRows() int {
	rows := d.height - 1 // footer
	if d.showHeader {
		rows-- // header
	}
	if d.err != "" {
		rows-- // error line
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (d *dashboard) expMaxLines() int {
	lines := d.height - 2 // title + help line
	if lines < 1 {
		lines = 1
	}
	return lines
}

func (d *dashboard) findSelected() int {
	for i, t := range d.tasks {
		if t.Id == d.selected {
			return i
		}
	}
	return -1
}

func (d *dashboard) selectedIdx() int {
	if idx := d.findSelected(); idx >= 0 {
		return idx
	}
	return 0
}

func (d *dashboard) selectedTask() *tes.Task {
	if idx := d.findSelected(); idx >= 0 {
		return d.tasks[idx]
	}
	return nil
}

func (d *dashboard) setSelectedIdx(idx int) {
	if idx >= 0 && idx < len(d.tasks) {
		d.selected = d.tasks[idx].Id
	}
}

func (d *dashboard) moveCursor(delta int) {
	idx := d.selectedIdx() + delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(d.tasks) {
		idx = len(d.tasks) - 1
	}
	if len(d.tasks) == 0 {
		return
	}
	d.setSelectedIdx(idx)
	d.clampOffset()
}

// clampOffset keeps the selected row within the visible window.
func (d *dashboard) clampOffset() {
	rows := d.maxRows()
	if d.offset > len(d.tasks)-1 {
		d.offset = len(d.tasks) - 1
	}
	if d.offset < 0 {
		d.offset = 0
	}
	idx := d.selectedIdx()
	if idx < d.offset {
		d.offset = idx
	}
	if idx >= d.offset+rows {
		d.offset = idx - rows + 1
	}
}

func indexOf(fields []string, f string) int {
	for i, v := range fields {
		if v == f {
			return i
		}
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
