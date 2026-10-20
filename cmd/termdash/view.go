package termdash

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/calypr/funnel/tes"
)

// styles
var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4")) // blue
	runningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red
	completeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red, matches old theme
	queuedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))            // white
	errTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
)

// View renders the dashboard.
func (d *dashboard) View() string {
	if d.width == 0 {
		return "loading..."
	}

	var b strings.Builder

	if d.showHeader && d.mode != modeExpanded {
		b.WriteString(d.headerView())
	}

	switch d.mode {
	case modeSort:
		b.WriteString(d.sortMenuView())
	case modeHelp:
		b.WriteString(d.helpView())
	case modeExpanded:
		b.WriteString(d.expandedView())
		return b.String()
	default:
		b.WriteString(d.tableView())
		if d.mode == modeFilter {
			b.WriteString(d.filterInputView())
		}
		b.WriteString(d.footerView())
	}
	return b.String()
}

// stateView renders a task state with the old theme's colors:
// RUNNING/INITIALIZING green, COMPLETE/EXECUTOR_ERROR/CANCELED/SYSTEM_ERROR red,
// QUEUED white, everything else dim.
func stateView(s tes.State) string {
	switch s {
	case tes.State_RUNNING, tes.State_INITIALIZING:
		return runningStyle.Render(s.String())
	case tes.State_COMPLETE, tes.State_EXECUTOR_ERROR, tes.State_CANCELED, tes.State_SYSTEM_ERROR:
		return errorStyle.Render(s.String())
	case tes.State_QUEUED:
		return queuedStyle.Render(s.String())
	default:
		return dimStyle.Render(s.String())
	}
}

func (d *dashboard) headerView() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s  %s\n", titleStyle.Render("Funnel Dashboard"), timeNow()))
	line := ""
	if d.source.PreviousPageToken() != "" {
		line += "<- Previous "
	}
	if d.source.NextPageToken() != "" {
		line += " Next ->"
	}
	if line != "" {
		b.WriteString(dimStyle.Render(strings.TrimSpace(line)) + "\n")
	}
	if d.filter != "" {
		b.WriteString(fmt.Sprintf("Filter: %s\n", d.filter))
	}
	if d.err != "" {
		b.WriteString(errTextStyle.Render(d.err) + "\n")
	}
	return b.String()
}

func (d *dashboard) tableView() string {
	var b strings.Builder
	if len(d.tasks) == 0 {
		b.WriteString("No tasks.\n")
		return b.String()
	}
	rows := d.maxRows()
	end := d.offset + rows
	if end > len(d.tasks) {
		end = len(d.tasks)
	}
	for i := d.offset; i < end; i++ {
		t := d.tasks[i]
		line := fmt.Sprintf("%-40s %-15s %s", truncate(t.Id, 40), stateView(t.State), truncate(t.Name, 60))
		if t.Id == d.selected {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (d *dashboard) footerView() string {
	sortDir := "asc"
	if d.reversed {
		sortDir = "desc"
	}
	return dimStyle.Render(fmt.Sprintf(
		"%d tasks | sort: %s (%s) | [j/k] move [enter] expand [f] filter [s] sort [r] reverse [a] all [n/p] page [h] help [q] quit",
		len(d.tasks), d.sortField, sortDir))
}

func (d *dashboard) filterInputView() string {
	return fmt.Sprintf("Filter (enter to apply, esc to clear): %s▏\n", d.filterBuf)
}

func (d *dashboard) sortMenuView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Sort tasks by:\n"))
	for i, f := range SortFields() {
		cursor := "  "
		if i == d.sortCursor {
			cursor = "> "
			b.WriteString(selectedStyle.Render(cursor+f) + "\n")
		} else {
			b.WriteString(cursor + f + "\n")
		}
	}
	b.WriteString(dimStyle.Render("[up/down] select [enter] apply [esc] cancel") + "\n")
	return b.String()
}

func (d *dashboard) helpView() string {
	return titleStyle.Render("Funnel Dashboard — Help") + `

  j/k, arrows      Move cursor
  pgup/pgdn        Scroll page
  g/home, G/end    Jump to first/last task
  enter            Expand task (full JSON)
  esc, q           Back / quit
  f                Filter tasks (enter applies, esc clears)
  a                Toggle showing all tasks
  s                Sort menu
  r                Reverse sort order
  n / p            Next / previous server page
  H                Toggle header

` + dimStyle.Render("press any key to return")
}

func (d *dashboard) expandedView() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s  %s\n", titleStyle.Render("Task "+d.expandedID), dimStyle.Render("[esc] back")))
	maxLines := d.expMaxLines()
	end := d.expOffset + maxLines
	if end > len(d.expandedJSON) {
		end = len(d.expandedJSON)
	}
	if d.expOffset < end {
		b.WriteString(strings.Join(d.expandedJSON[d.expOffset:end], "\n"))
	}
	if end < len(d.expandedJSON) {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("--- more (%d/%d lines, pgup/pgdn to scroll) ---", end, len(d.expandedJSON))))
	}
	return b.String() + "\n"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func taskJSON(t *tes.Task) string {
	data, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(t)
	if err != nil {
		return fmt.Sprintf("error formatting task: %v", err)
	}
	return string(data)
}
