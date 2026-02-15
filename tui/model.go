package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/mickamy/grpc-tap/clipboard"
	tapv1 "github.com/mickamy/grpc-tap/gen/tap/v1"
	"github.com/mickamy/grpc-tap/proxy"
)

type viewMode int

const (
	viewList viewMode = iota
	viewInspect
	viewAnalytics
)

type sortMode int

const (
	sortChronological sortMode = iota
	sortDuration
)

// Model is the Bubble Tea model for the grpc-tap TUI.
type Model struct {
	target string
	conn   *grpc.ClientConn
	client tapv1.TapServiceClient
	stream tapv1.TapService_WatchClient

	events []*tapv1.GRPCEvent
	cursor int
	follow bool
	width  int
	height int
	err    error
	view   viewMode

	searchMode   bool
	searchQuery  string
	sortMode     sortMode
	filterErrors bool

	displayRows []int // indices into events

	inspectScroll int
	inspectStatus string // temporary status message (e.g. "Copied!")
	replayEventID string // when set, navigate to this event in inspector on arrival

	analyticsRows     []analyticsRow
	analyticsCursor   int
	analyticsSortMode analyticsSortMode
}

type eventMsg struct{ Event *tapv1.GRPCEvent }
type errMsg struct{ Err error }

type connectedMsg struct {
	conn   *grpc.ClientConn
	client tapv1.TapServiceClient
	stream tapv1.TapService_WatchClient
}

type replayResultMsg struct {
	EventID string // ID of the replayed event (empty on error)
	Err     error
}

type clearStatusMsg struct{}

// New creates a new Model targeting the given grpc-tapd address.
func New(target string) Model {
	return Model{
		target: target,
		follow: true,
	}
}

func (m Model) Init() tea.Cmd {
	return connectCmd(m.target)
}

func connectCmd(target string) tea.Cmd {
	return func() tea.Msg {
		conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return errMsg{Err: fmt.Errorf("dial %s: %w", target, err)}
		}
		client := tapv1.NewTapServiceClient(conn)
		stream, err := client.Watch(context.Background(), &tapv1.WatchRequest{})
		if err != nil {
			_ = conn.Close()
			return errMsg{Err: fmt.Errorf("watch %s: %w", target, err)}
		}
		return connectedMsg{conn: conn, client: client, stream: stream}
	}
}

func recvEvent(stream tapv1.TapService_WatchClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := stream.Recv()
		if err != nil {
			return errMsg{Err: err}
		}
		return eventMsg{Event: resp.GetEvent()}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectedMsg:
		m.conn = msg.conn
		m.client = msg.client
		m.stream = msg.stream
		return m, recvEvent(msg.stream)

	case eventMsg:
		m.events = append(m.events, msg.Event)
		if m.replayEventID != "" && msg.Event.GetId() == m.replayEventID {
			// Replayed event arrived — show it in inspector.
			m.replayEventID = ""
			m.displayRows = m.rebuildDisplayRows()
			m.cursor = max(len(m.displayRows)-1, 0)
			m.view = viewInspect
			m.inspectScroll = 0
		} else if m.view == viewList {
			m.displayRows = m.rebuildDisplayRows()
			if m.follow {
				m.cursor = max(len(m.displayRows)-1, 0)
			}
		}
		return m, recvEvent(m.stream)

	case replayResultMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		// Wait for the replayed event to arrive via Watch stream, then show in inspector.
		m.replayEventID = msg.EventID
		return m, nil

	case clearStatusMsg:
		m.inspectStatus = ""
		return m, nil

	case errMsg:
		m.err = msg.Err
		return m, nil

	case tea.KeyMsg:
		switch m.view {
		case viewAnalytics:
			return m.updateAnalytics(msg)
		case viewInspect:
			return m.updateInspect(msg)
		case viewList:
			return m.updateList(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.err != nil {
		return friendlyError(m.err, m.width)
	}

	if len(m.events) == 0 {
		return "Waiting for gRPC traffic..."
	}

	switch m.view {
	case viewAnalytics:
		return m.renderAnalytics()
	case viewInspect:
		return m.renderInspector()
	}

	return m.renderListView()
}

func (m Model) listHeight() int {
	return max(m.height-8, 3)
}

func (m Model) rebuildDisplayRows() []int {
	var rows []int
	filter := strings.ToLower(m.searchQuery)

	for i, ev := range m.events {
		if filter != "" && !strings.Contains(strings.ToLower(ev.GetMethod()), filter) {
			continue
		}
		if m.filterErrors && ev.GetStatus() == 0 {
			continue
		}
		rows = append(rows, i)
	}

	if m.sortMode == sortDuration {
		sort.Slice(rows, func(a, b int) bool {
			da := m.events[rows[a]].GetDuration().AsDuration()
			db := m.events[rows[b]].GetDuration().AsDuration()
			return da > db
		})
	}

	return rows
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchMode {
		return m.updateSearch(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	case "enter":
		if len(m.displayRows) > 0 {
			m.view = viewInspect
			m.inspectScroll = 0
		}
		return m, nil
	case "/":
		m.searchMode = true
		m.searchQuery = ""
		return m, nil
	case "e":
		m.filterErrors = !m.filterErrors
		m.displayRows = m.rebuildDisplayRows()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "a":
		m.view = viewAnalytics
		m.analyticsRows = m.buildAnalyticsRows()
		sortAnalyticsRows(m.analyticsRows, m.analyticsSortMode)
		m.analyticsCursor = 0
		return m, nil
	case "s":
		return m.toggleSort(), nil
	case "esc":
		return m.clearFilter(), nil
	case "j", "down":
		if len(m.displayRows) > 0 && m.cursor < len(m.displayRows)-1 {
			m.cursor++
		}
		if len(m.displayRows) > 0 && m.cursor == len(m.displayRows)-1 {
			m.follow = true
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.follow = false
		}
		return m, nil
	case "ctrl+d", "pgdown":
		half := max(m.listHeight()/2, 1)
		m.cursor = min(m.cursor+half, max(len(m.displayRows)-1, 0))
		if len(m.displayRows) > 0 && m.cursor == len(m.displayRows)-1 {
			m.follow = true
		}
		return m, nil
	case "ctrl+u", "pgup":
		half := max(m.listHeight()/2, 1)
		m.cursor = max(m.cursor-half, 0)
		m.follow = false
		return m, nil
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searchMode = false
		return m, nil
	case "esc":
		m.searchMode = false
		m.searchQuery = ""
		m.displayRows = m.rebuildDisplayRows()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		return m, nil
	case "backspace":
		if len(m.searchQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.searchQuery)
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-size]
			m.displayRows = m.rebuildDisplayRows()
			m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
		}
		return m, nil
	case "ctrl+c":
		if m.conn != nil {
			_ = m.conn.Close()
		}
		return m, tea.Quit
	}

	r := msg.Runes
	if len(r) == 0 {
		return m, nil
	}

	m.searchQuery += string(r)
	m.displayRows = m.rebuildDisplayRows()
	m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
	return m, nil
}

func (m Model) toggleSort() Model {
	switch m.sortMode {
	case sortChronological:
		m.sortMode = sortDuration
		m.follow = false
	case sortDuration:
		m.sortMode = sortChronological
	}
	m.displayRows = m.rebuildDisplayRows()
	m.cursor = 0
	return m
}

func (m Model) clearFilter() Model {
	if m.searchQuery != "" {
		m.searchQuery = ""
		m.displayRows = m.rebuildDisplayRows()
		m.cursor = min(m.cursor, max(len(m.displayRows)-1, 0))
	}
	return m
}

func (m Model) cursorEvent() *tapv1.GRPCEvent {
	if m.cursor < 0 || m.cursor >= len(m.displayRows) {
		return nil
	}
	return m.events[m.displayRows[m.cursor]]
}

// renderListView renders the main list + preview + footer.
func (m Model) renderListView() string {
	innerWidth := max(m.width-4, 20)
	listHeight := m.listHeight()

	// Title
	var title string
	if m.searchQuery != "" {
		title = fmt.Sprintf(" grpc-tap (%d/%d events) ", len(m.displayRows), len(m.events))
	} else {
		title = fmt.Sprintf(" grpc-tap (%d events) ", len(m.events))
	}
	if m.filterErrors {
		title += "[errors] "
	}
	if m.sortMode == sortDuration {
		title += "[slow] "
	}

	// Column widths
	colMarker := 4
	colProto := 10
	colStatus := 10
	colDuration := 10
	colTime := 13
	colMethod := max(innerWidth-colMarker-colProto-colStatus-colDuration-colTime-4, 10)

	// Header
	header := fmt.Sprintf("    %-*s %-*s %-*s %*s %*s",
		colProto, "Proto",
		colMethod, "Method",
		colStatus, "Status",
		colDuration, "Duration",
		colTime, "Time",
	)

	// Visible rows
	dataRows := max(listHeight-1, 1)
	start := 0
	if len(m.displayRows) > dataRows {
		start = max(m.cursor-dataRows/2, 0)
		if start+dataRows > len(m.displayRows) {
			start = len(m.displayRows) - dataRows
		}
	}
	end := min(start+dataRows, len(m.displayRows))

	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Render(header))
	for i := start; i < end; i++ {
		ev := m.events[m.displayRows[i]]
		isCursor := i == m.cursor

		marker := "  "
		if isCursor {
			marker = "▶ "
		}

		proto := protocolString(int32(ev.GetProtocol()))
		method := truncate(ev.GetMethod(), colMethod)
		status := statusString(ev.GetStatus())
		dur := formatDuration(ev.GetDuration())
		t := formatTime(ev.GetStartTime())

		stStyle := statusStyle(ev.GetStatus())
		if isCursor {
			bold := lipgloss.NewStyle().Bold(true)
			stStyle = stStyle.Bold(true)
			row := fmt.Sprintf("%s  %s %s %s %s %s",
				bold.Render(marker),
				padRight(bold.Render(proto), colProto),
				padRight(bold.Render(method), colMethod),
				padRight(stStyle.Render(status), colStatus),
				padLeft(bold.Render(dur), colDuration),
				padLeft(bold.Render(t), colTime),
			)
			rows = append(rows, row)
			continue
		}
		row := fmt.Sprintf("%s  %-*s %-*s %s %*s %*s",
			marker,
			colProto, proto,
			colMethod, method,
			padRight(stStyle.Render(status), colStatus),
			colDuration, dur,
			colTime, t,
		)
		rows = append(rows, row)
	}

	// Border
	borderColor := lipgloss.Color("240")
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(borderColor)

	content := strings.Join(rows, "\n")
	box := border.Render(content)

	// Replace top border with title
	lines := strings.Split(box, "\n")
	if len(lines) > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		titleStyle := lipgloss.NewStyle().Bold(true)
		dashes := max(innerWidth-len([]rune(title)), 0)
		lines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
		box = strings.Join(lines, "\n")
	}

	// Preview
	preview := m.renderPreview(innerWidth)

	// Footer
	var footer string
	switch {
	case m.searchMode:
		footer = fmt.Sprintf("  / %s█", m.searchQuery)
	default:
		footer = "  q: quit  j/k: navigate  enter: inspect  /: search  s: sort  e: errors  a: analytics"
		if m.searchQuery != "" {
			footer += "  esc: clear filter"
		}
		if m.sortMode == sortDuration {
			footer += "  [sorted: duration]"
		}
	}

	return strings.Join([]string{box, preview, footer}, "\n")
}

// renderPreview renders the bottom preview pane.
func (m Model) renderPreview(innerWidth int) string {
	ev := m.cursorEvent()
	if ev == nil {
		return ""
	}

	var lines []string
	lines = append(lines, "Method:   "+ev.GetMethod())
	lines = append(lines, "Protocol: "+protocolString(int32(ev.GetProtocol())))
	lines = append(lines, "Status:   "+statusString(ev.GetStatus()))
	lines = append(lines, "Duration: "+formatDuration(ev.GetDuration()))
	if ev.GetError() != "" {
		lines = append(lines, "Error:    "+ev.GetError())
	}

	content := strings.Join(lines, "\n")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(innerWidth).
		BorderForeground(lipgloss.Color("240"))

	return border.Render(content)
}

// renderInspector renders the full-screen inspector view.
func (m Model) renderInspector() string {
	ev := m.cursorEvent()
	if ev == nil {
		return ""
	}

	innerWidth := max(m.width-4, 20)
	visibleRows := max(m.height-2, 3)

	lines := m.inspectLines(ev)

	maxScroll := max(len(lines)-visibleRows, 0)
	if m.inspectScroll > maxScroll {
		m.inspectScroll = maxScroll
	}

	end := min(m.inspectScroll+visibleRows, len(lines))
	visible := lines[m.inspectScroll:end]
	content := strings.Join(visible, "\n")

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
		title := " Inspector "
		if m.inspectStatus != "" {
			title += "— " + m.inspectStatus + " "
		}
		dashes := max(innerWidth-len([]rune(title)), 0)
		boxLines[0] = borderFg.Render("╭") +
			titleStyle.Render(title) +
			borderFg.Render(strings.Repeat("─", dashes)+"╮")
	}
	if n := len(boxLines); n > 0 {
		borderFg := lipgloss.NewStyle().Foreground(borderColor)
		help := " q: back  j/k: scroll  c/C: copy req/resp  e: edit & resend "
		dashes := max(innerWidth-len([]rune(help)), 0)
		boxLines[n-1] = borderFg.Render("╰") +
			lipgloss.NewStyle().Faint(true).Render(help) +
			borderFg.Render(strings.Repeat("─", dashes)+"╯")
	}

	return strings.Join(boxLines, "\n")
}

func (m Model) inspectLines(ev *tapv1.GRPCEvent) []string {
	var lines []string
	lines = append(lines, "Method:   "+ev.GetMethod())
	lines = append(lines, "Protocol: "+protocolString(int32(ev.GetProtocol())))
	lines = append(lines, "Status:   "+statusString(ev.GetStatus()))
	lines = append(lines, "Duration: "+formatDuration(ev.GetDuration()))
	lines = append(lines, "Time:     "+formatTime(ev.GetStartTime()))
	lines = append(lines, "ID:       "+ev.GetId())
	if ev.GetError() != "" {
		lines = append(lines, "Error:    "+ev.GetError())
	}
	if len(ev.GetRequestHeaders()) > 0 {
		lines = append(lines, "")
		lines = append(lines, "── Request Headers ──")
		lines = append(lines, formatHeaders(ev.GetRequestHeaders())...)
	}
	if len(ev.GetResponseHeaders()) > 0 {
		lines = append(lines, "")
		lines = append(lines, "── Response Headers ──")
		lines = append(lines, formatHeaders(ev.GetResponseHeaders())...)
	}
	if len(ev.GetRequestBody()) > 0 {
		lines = append(lines, "")
		lines = append(lines, "── Request Body ──")
		lines = append(lines, formatBody(ev.GetRequestBody())...)
	}
	if len(ev.GetResponseBody()) > 0 {
		lines = append(lines, "")
		lines = append(lines, "── Response Body ──")
		lines = append(lines, formatBody(ev.GetResponseBody())...)
	}
	return lines
}

func (m Model) updateInspect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "e":
		ev := m.cursorEvent()
		if ev == nil || len(ev.GetRequestBody()) == 0 || m.client == nil {
			return m, nil
		}
		return m, m.editAndResend(ev)
	case "c":
		ev := m.cursorEvent()
		if ev == nil || len(ev.GetRequestBody()) == 0 {
			return m, nil
		}
		return m.copyBody(ev.GetRequestBody(), "Request copied!")
	case "C":
		ev := m.cursorEvent()
		if ev == nil || len(ev.GetResponseBody()) == 0 {
			return m, nil
		}
		return m.copyBody(ev.GetResponseBody(), "Response copied!")
	case "j", "down":
		ev := m.cursorEvent()
		if ev != nil {
			maxScroll := max(len(m.inspectLines(ev))-(m.height-2), 0)
			if m.inspectScroll < maxScroll {
				m.inspectScroll++
			}
		}
		return m, nil
	case "k", "up":
		if m.inspectScroll > 0 {
			m.inspectScroll--
		}
		return m, nil
	}
	return m, nil
}

func (m Model) editAndResend(ev *tapv1.GRPCEvent) tea.Cmd {
	method := ev.GetMethod()
	body := ev.GetRequestBody()

	// Convert request body to JSON for editing.
	jsonData, err := proxy.ProtoWireToJSON(body)
	if err != nil {
		// Fall back to raw hex-ish representation; not ideal but usable.
		return func() tea.Msg { return replayResultMsg{Err: fmt.Errorf("encode JSON: %w", err)} }
	}

	// Write to temp file.
	tmpFile, err := os.CreateTemp("", "grpc-tap-*.json")
	if err != nil {
		return func() tea.Msg { return replayResultMsg{Err: fmt.Errorf("create temp file: %w", err)} }
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(jsonData); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return func() tea.Msg { return replayResultMsg{Err: fmt.Errorf("write temp file: %w", err)} }
	}
	_ = tmpFile.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	client := m.client

	// Use tea.ExecProcess to open the editor.
	c := exec.Command(editor, tmpPath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer func() { _ = os.Remove(tmpPath) }()
		if err != nil {
			return replayResultMsg{Err: fmt.Errorf("editor: %w", err)}
		}

		// Read edited file.
		edited, err := os.ReadFile(tmpPath)
		if err != nil {
			return replayResultMsg{Err: fmt.Errorf("read edited file: %w", err)}
		}

		// Convert JSON back to protobuf wire format.
		wire, err := proxy.JSONToProtoWire(edited)
		if err != nil {
			return replayResultMsg{Err: fmt.Errorf("encode protobuf: %w", err)}
		}

		// Call Replay RPC.
		resp, err := client.Replay(context.Background(), &tapv1.ReplayRequest{
			Method:      method,
			RequestBody: wire,
		})
		if err != nil {
			return replayResultMsg{Err: fmt.Errorf("replay: %w", err)}
		}

		return replayResultMsg{EventID: resp.GetEvent().GetId()}
	})
}

func (m Model) copyBody(body []byte, statusText string) (tea.Model, tea.Cmd) {
	text := bodyToClipboardText(body)
	if err := clipboard.Copy(context.Background(), text); err != nil {
		m.inspectStatus = "Copy failed"
	} else {
		m.inspectStatus = statusText
	}
	return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

func bodyToClipboardText(body []byte) string {
	// Try JSON (pretty-printed protobuf wire)
	if j, err := proxy.ProtoWireToJSON(body); err == nil {
		return string(j)
	}
	// Fall back to raw string
	return string(body)
}
