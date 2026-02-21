package tui

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tapv1 "github.com/mickamy/grpc-tap/gen/tap/v1"
)

type exportFormat int

const (
	exportJSON exportFormat = iota
	exportMarkdown
)

func (f exportFormat) ext() string {
	if f == exportMarkdown {
		return "md"
	}
	return "json"
}

type exportCall struct {
	Time       string  `json:"time"`
	Method     string  `json:"method"`
	CallType   string  `json:"call_type"`
	Protocol   string  `json:"protocol"`
	DurationMs float64 `json:"duration_ms"`
	Status     int32   `json:"status"`
	Error      string  `json:"error"`
}

type exportAnalyticsRow struct {
	Method  string  `json:"method"`
	Count   int     `json:"count"`
	Errors  int     `json:"errors"`
	TotalMs float64 `json:"total_ms"`
	AvgMs   float64 `json:"avg_ms"`
	P95Ms   float64 `json:"p95_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type exportData struct {
	Captured int    `json:"captured"`
	Exported int    `json:"exported"`
	Search   string `json:"search"`
	Period   struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"period"`
	Calls     []exportCall         `json:"calls"`
	Analytics []exportAnalyticsRow `json:"analytics"`
}

func filteredExportEvents(
	events []*tapv1.GRPCEvent, searchQuery string, filterErrors bool,
) []*tapv1.GRPCEvent {
	filter := strings.ToLower(searchQuery)
	result := make([]*tapv1.GRPCEvent, 0, len(events))
	for _, ev := range events {
		if filter != "" && !strings.Contains(strings.ToLower(ev.GetMethod()), filter) {
			continue
		}
		if filterErrors && ev.GetStatus() == 0 {
			continue
		}
		result = append(result, ev)
	}
	return result
}

func buildExportAnalyticsRows(events []*tapv1.GRPCEvent) []exportAnalyticsRow {
	type agg struct {
		count     int
		errors    int
		totalDur  time.Duration
		durations []time.Duration
	}
	groups := make(map[string]*agg)
	var order []string

	for _, ev := range events {
		method := ev.GetMethod()
		if method == "" {
			continue
		}
		dur := ev.GetDuration().AsDuration()
		g, ok := groups[method]
		if !ok {
			g = &agg{}
			groups[method] = g
			order = append(order, method)
		}
		g.count++
		g.totalDur += dur
		g.durations = append(g.durations, dur)
		if ev.GetStatus() != 0 {
			g.errors++
		}
	}

	rows := make([]exportAnalyticsRow, 0, len(groups))
	for _, method := range order {
		g := groups[method]
		slices.SortFunc(g.durations, cmp.Compare)
		totalMs := float64(g.totalDur.Microseconds()) / 1000
		avgMs := totalMs / float64(g.count)
		p95Ms := float64(percentile(g.durations, 0.95).Microseconds()) / 1000
		maxMs := float64(g.durations[len(g.durations)-1].Microseconds()) / 1000
		rows = append(rows, exportAnalyticsRow{
			Method:  method,
			Count:   g.count,
			Errors:  g.errors,
			TotalMs: totalMs,
			AvgMs:   avgMs,
			P95Ms:   p95Ms,
			MaxMs:   maxMs,
		})
	}
	return rows
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func buildExportDataFromEvents(
	allEvents []*tapv1.GRPCEvent, searchQuery string, filterErrors bool,
) exportData {
	exported := filteredExportEvents(allEvents, searchQuery, filterErrors)

	var d exportData
	d.Captured = len(allEvents)
	d.Exported = len(exported)
	d.Search = searchQuery

	if len(exported) > 0 {
		first := exported[0].GetStartTime()
		last := exported[len(exported)-1].GetStartTime()
		//nolint:gosmopolitan // export uses local time
		d.Period.Start = first.AsTime().In(time.Local).Format("15:04:05")
		//nolint:gosmopolitan // export uses local time
		d.Period.End = last.AsTime().In(time.Local).Format("15:04:05")
	}

	d.Calls = make([]exportCall, 0, len(exported))
	for _, ev := range exported {
		var durMs float64
		if dur := ev.GetDuration(); dur != nil {
			durMs = float64(dur.AsDuration().Microseconds()) / 1000
		}
		//nolint:gosmopolitan // export uses local time
		ts := ev.GetStartTime().AsTime().In(time.Local)
		d.Calls = append(d.Calls, exportCall{
			Time:       ts.Format("15:04:05.000"),
			Method:     ev.GetMethod(),
			CallType:   callTypeString(ev.GetCallType()),
			Protocol:   protocolString(int32(ev.GetProtocol())),
			DurationMs: durMs,
			Status:     ev.GetStatus(),
			Error:      ev.GetError(),
		})
	}

	d.Analytics = buildExportAnalyticsRows(exported)
	return d
}

func callTypeString(ct tapv1.CallType) string {
	switch ct {
	case tapv1.CallType_CALL_TYPE_UNARY:
		return "Unary"
	case tapv1.CallType_CALL_TYPE_SERVER_STREAM:
		return "ServerStream"
	case tapv1.CallType_CALL_TYPE_CLIENT_STREAM:
		return "ClientStream"
	case tapv1.CallType_CALL_TYPE_BIDI_STREAM:
		return "BidiStream"
	default:
		return "Unknown"
	}
}

func renderExportJSON(
	allEvents []*tapv1.GRPCEvent, searchQuery string, filterErrors bool,
) (string, error) {
	d := buildExportDataFromEvents(allEvents, searchQuery, filterErrors)
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export: %w", err)
	}
	return string(b) + "\n", nil
}

func renderExportMarkdown(
	allEvents []*tapv1.GRPCEvent, searchQuery string, filterErrors bool,
) string {
	d := buildExportDataFromEvents(allEvents, searchQuery, filterErrors)

	var sb strings.Builder
	sb.WriteString("# grpc-tap export\n\n")

	fmt.Fprintf(&sb, "- Captured: %d calls\n", d.Captured)
	exportLine := fmt.Sprintf("- Exported: %d calls", d.Exported)
	if d.Search != "" {
		exportLine += " (search: " + d.Search + ")"
	}
	sb.WriteString(exportLine + "\n")
	if d.Period.Start != "" {
		fmt.Fprintf(&sb, "- Period: %s — %s\n",
			d.Period.Start, d.Period.End)
	}

	sb.WriteString("\n## Calls\n\n")
	sb.WriteString("| # | Time | Method | Type | Protocol | Duration | Status | Error |\n")
	sb.WriteString("|---|------|--------|------|----------|----------|--------|-------|\n")
	for i, c := range d.Calls {
		fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s | %s | %s | %s |\n",
			i+1, c.Time,
			escapeMarkdownPipe(c.Method),
			c.CallType, c.Protocol,
			formatDurationMs(c.DurationMs),
			formatStatusMarkdown(c.Status),
			escapeMarkdownPipe(c.Error),
		)
	}

	if len(d.Analytics) > 0 {
		sb.WriteString("\n## Analytics\n\n")
		sb.WriteString("| Method | Count | Errors | Avg | P95 | Max | Total |\n")
		sb.WriteString("|--------|-------|--------|-----|-----|-----|-------|\n")
		for _, a := range d.Analytics {
			errStr := "0"
			if a.Errors > 0 {
				errStr = fmt.Sprintf("%d(%.0f%%)", a.Errors, float64(a.Errors)/float64(a.Count)*100)
			}
			fmt.Fprintf(&sb, "| %s | %d | %s | %s | %s | %s | %s |\n",
				escapeMarkdownPipe(a.Method),
				a.Count,
				errStr,
				formatDurationMs(a.AvgMs),
				formatDurationMs(a.P95Ms),
				formatDurationMs(a.MaxMs),
				formatDurationMs(a.TotalMs),
			)
		}
	}

	return sb.String()
}

func formatDurationMs(ms float64) string {
	switch {
	case ms < 1:
		return fmt.Sprintf("%.0fµs", ms*1000)
	case ms < 1000:
		return fmt.Sprintf("%.1fms", ms)
	default:
		return fmt.Sprintf("%.2fs", ms/1000)
	}
}

func formatStatusMarkdown(status int32) string {
	if status == 0 {
		return "OK"
	}
	return fmt.Sprintf("ERR(%d)", status)
}

func escapeMarkdownPipe(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// writeExport writes filtered events to a file and returns the path.
// dir specifies the output directory; if empty, the current directory is used.
func writeExport(
	allEvents []*tapv1.GRPCEvent,
	searchQuery string,
	filterErrors bool,
	format exportFormat,
	dir string,
) (string, error) {
	var content string
	var err error

	switch format {
	case exportJSON:
		content, err = renderExportJSON(allEvents, searchQuery, filterErrors)
		if err != nil {
			return "", err
		}
	case exportMarkdown:
		content = renderExportMarkdown(allEvents, searchQuery, filterErrors)
	}

	filename := fmt.Sprintf("grpc-tap-%s.%s",
		time.Now().Format("20060102-150405"), format.ext())
	if dir != "" {
		filename = filepath.Join(dir, filename)
	}

	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write export: %w", err)
	}
	return filename, nil
}
