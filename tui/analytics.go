package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type analyticsSortMode int

const (
	analyticsSortTotalDuration analyticsSortMode = iota
	analyticsSortCount
	analyticsSortAvgDuration
	analyticsSortErrorRate
)

func (s analyticsSortMode) String() string {
	switch s {
	case analyticsSortTotalDuration:
		return "total"
	case analyticsSortCount:
		return "count"
	case analyticsSortAvgDuration:
		return "avg"
	case analyticsSortErrorRate:
		return "errors"
	}
	return "total"
}

func (s analyticsSortMode) next() analyticsSortMode {
	switch s {
	case analyticsSortTotalDuration:
		return analyticsSortCount
	case analyticsSortCount:
		return analyticsSortAvgDuration
	case analyticsSortAvgDuration:
		return analyticsSortErrorRate
	case analyticsSortErrorRate:
		return analyticsSortTotalDuration
	}
	return analyticsSortTotalDuration
}

type analyticsRow struct {
	method        string
	count         int
	errors        int
	totalDuration time.Duration
	avgDuration   time.Duration
}

func (r analyticsRow) errorRate() float64 {
	if r.count == 0 {
		return 0
	}
	return float64(r.errors) / float64(r.count) * 100
}

func (m Model) buildAnalyticsRows() []analyticsRow {
	type agg struct {
		count    int
		errors   int
		totalDur time.Duration
	}
	groups := make(map[string]*agg)

	for _, ev := range m.events {
		method := ev.GetMethod()
		if method == "" {
			continue
		}

		g, ok := groups[method]
		if !ok {
			g = &agg{}
			groups[method] = g
		}
		g.count++
		g.totalDur += ev.GetDuration().AsDuration()
		if ev.GetStatus() != 0 {
			g.errors++
		}
	}

	rows := make([]analyticsRow, 0, len(groups))
	for method, g := range groups {
		rows = append(rows, analyticsRow{
			method:        method,
			count:         g.count,
			errors:        g.errors,
			totalDuration: g.totalDur,
			avgDuration:   g.totalDur / time.Duration(g.count),
		})
	}
	return rows
}

func sortAnalyticsRows(rows []analyticsRow, mode analyticsSortMode) {
	sort.Slice(rows, func(i, j int) bool {
		switch mode {
		case analyticsSortTotalDuration:
			return rows[i].totalDuration > rows[j].totalDuration
		case analyticsSortCount:
			return rows[i].count > rows[j].count
		case analyticsSortAvgDuration:
			return rows[i].avgDuration > rows[j].avgDuration
		case analyticsSortErrorRate:
			return rows[i].errorRate() > rows[j].errorRate()
		}
		return rows[i].totalDuration > rows[j].totalDuration
	})
}

func (m Model) updateAnalytics(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "q":
		m.view = viewList
		m.displayRows = m.rebuildDisplayRows()
		if m.follow {
			m.cursor = max(len(m.displayRows)-1, 0)
		}
		return m, nil
	case "j", "down":
		if len(m.analyticsRows) > 0 && m.analyticsCursor < len(m.analyticsRows)-1 {
			m.analyticsCursor++
		}
		return m, nil
	case "k", "up":
		if m.analyticsCursor > 0 {
			m.analyticsCursor--
		}
		return m, nil
	case "ctrl+d", "pgdown":
		half := m.analyticsVisibleRows() / 2
		m.analyticsCursor = min(m.analyticsCursor+half, max(len(m.analyticsRows)-1, 0))
		return m, nil
	case "ctrl+u", "pgup":
		half := m.analyticsVisibleRows() / 2
		m.analyticsCursor = max(m.analyticsCursor-half, 0)
		return m, nil
	case "s":
		m.analyticsSortMode = m.analyticsSortMode.next()
		sortAnalyticsRows(m.analyticsRows, m.analyticsSortMode)
		m.analyticsCursor = 0
		return m, nil
	}
	return m, nil
}

const (
	analyticsColMarker = 2
	analyticsColCount  = 7
	analyticsColErrors = 8
	analyticsColAvg    = 10
	analyticsColTotal  = 10
)

func (m Model) analyticsVisibleRows() int {
	return max(m.height-4, 3)
}

func (m Model) renderAnalytics() string {
	innerWidth := max(m.width-4, 20)
	visibleRows := m.analyticsVisibleRows()

	title := fmt.Sprintf(" Analytics (%d methods) [sort: %s] ", len(m.analyticsRows), m.analyticsSortMode)

	fixedCols := analyticsColMarker + analyticsColCount + analyticsColErrors + analyticsColAvg + analyticsColTotal + 4
	colMethod := max(innerWidth-fixedCols, 10)

	header := fmt.Sprintf("  %*s %*s %*s %*s  %s",
		analyticsColCount, "Count",
		analyticsColErrors, "Errors",
		analyticsColAvg, "Avg",
		analyticsColTotal, "Total",
		"Method",
	)

	dataRows := max(visibleRows-1, 1)

	start := 0
	if len(m.analyticsRows) > dataRows {
		start = max(m.analyticsCursor-dataRows/2, 0)
		if start+dataRows > len(m.analyticsRows) {
			start = len(m.analyticsRows) - dataRows
		}
	}
	end := min(start+dataRows, len(m.analyticsRows))

	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render(header))
	for i := start; i < end; i++ {
		r := m.analyticsRows[i]
		marker := "  "
		if i == m.analyticsCursor {
			marker = "▶ "
		}

		method := truncate(r.method, colMethod)

		errStr := strconv.Itoa(r.errors)
		if r.errors > 0 {
			errStr = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(
				fmt.Sprintf("%d(%.0f%%)", r.errors, r.errorRate()),
			)
		}

		row := fmt.Sprintf("%s%*d %s %s %s  %s",
			marker,
			analyticsColCount, r.count,
			padLeft(errStr, analyticsColErrors),
			padLeft(formatDurationValue(r.avgDuration), analyticsColAvg),
			padLeft(formatDurationValue(r.totalDuration), analyticsColTotal),
			method,
		)
		if i == m.analyticsCursor {
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		rows = append(rows, row)
	}

	content := strings.Join(rows, "\n")

	borderColor := lipgloss.Color("240")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(borderColor).
		Render(content)

	boxLines := strings.Split(box, "\n")
	if len(boxLines) > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		titleStyle := lipgloss.NewStyle().Bold(true)
		dashes := max(innerWidth-len([]rune(title)), 0)
		boxLines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
	}

	if n := len(boxLines); n > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		help := " q: back  j/k: scroll  s: sort "
		dashes := max(innerWidth-len([]rune(help)), 0)
		boxLines[n-1] = borderFg.Render("╰") +
			lipgloss.NewStyle().Faint(true).Render(help) +
			borderFg.Render(strings.Repeat("─", dashes)+"╯")
	}

	return strings.Join(boxLines, "\n")
}
