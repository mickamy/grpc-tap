package tui

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func formatDuration(d *durationpb.Duration) string {
	if d == nil {
		return "-"
	}
	return formatDurationValue(d.AsDuration())
}

func formatDurationValue(dur time.Duration) string {
	switch {
	case dur < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(dur.Microseconds()))
	case dur < time.Second:
		return fmt.Sprintf("%.1fms", float64(dur.Microseconds())/1000)
	default:
		return fmt.Sprintf("%.2fs", dur.Seconds())
	}
}

func formatTime(t *timestamppb.Timestamp) string {
	if t == nil {
		return "-"
	}
	return t.AsTime().In(time.Local).Format("15:04:05.000") //nolint:gosmopolitan
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func padLeft(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func friendlyError(err error, width int) string {
	msg := err.Error()

	var text string
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "Unavailable"):
		text = "Could not connect to grpc-tapd.\n" +
			"Is grpc-tapd running?\n\n" +
			"Error: " + msg
	default:
		text = "Error: " + msg
	}

	return lipgloss.NewStyle().Width(width).Render(text)
}

func statusStyle(status int32) lipgloss.Style {
	if status == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
}

func statusString(status int32) string {
	if status == 0 {
		return "OK"
	}
	return fmt.Sprintf("ERR(%d)", status)
}

func formatBody(data []byte) []string {
	if lines := decodeProtoWire(data, ""); lines != nil {
		return lines
	}
	if utf8.Valid(data) {
		s := strings.TrimSpace(string(data))
		return strings.Split(s, "\n")
	}
	dump := hex.Dump(data)
	return strings.Split(strings.TrimRight(dump, "\n"), "\n")
}

func decodeProtoWire(data []byte, indent string) []string {
	if len(data) == 0 {
		return nil
	}
	var lines []string
	for len(data) > 0 {
		num, wtype, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil
		}
		data = data[n:]
		switch wtype {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil
			}
			data = data[n:]
			lines = append(lines, fmt.Sprintf("%s%d: %d", indent, num, v))
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(data)
			if n < 0 {
				return nil
			}
			data = data[n:]
			lines = append(lines, fmt.Sprintf("%s%d: %d", indent, num, v))
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(data)
			if n < 0 {
				return nil
			}
			data = data[n:]
			lines = append(lines, fmt.Sprintf("%s%d: %d", indent, num, v))
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil
			}
			data = data[n:]
			// Try nested message first
			if nested := decodeProtoWire(v, indent+"  "); nested != nil {
				lines = append(lines, fmt.Sprintf("%s%d: {", indent, num))
				lines = append(lines, nested...)
				lines = append(lines, indent+"}")
			} else if utf8.Valid(v) && isPrintable(v) {
				lines = append(lines, fmt.Sprintf("%s%d: %q", indent, num, string(v)))
			} else {
				lines = append(lines, fmt.Sprintf("%s%d: %x", indent, num, v))
			}
		case protowire.StartGroupType, protowire.EndGroupType:
			return nil
		}
	}
	return lines
}

func isPrintable(data []byte) bool {
	for _, b := range data {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			return false
		}
	}
	return true
}

func formatHeaders(headers map[string]string) []string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+": "+headers[k])
	}
	return lines
}

func protocolString(p int32) string {
	switch p {
	case 1:
		return "gRPC"
	case 2:
		return "gRPC-Web"
	case 3:
		return "Connect"
	default:
		return "Unknown"
	}
}

func overlayAlert(bg, msg string, width int) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("2")).
		Padding(0, 2).
		Render(msg)

	fgLines := strings.Split(box, "\n")
	bgLines := strings.Split(bg, "\n")

	startY := max((len(bgLines)-len(fgLines))/2, 0)
	for i, fl := range fgLines {
		y := startY + i
		if y >= len(bgLines) {
			break
		}
		fw := lipgloss.Width(fl)
		pad := max((width-fw)/2, 0)
		left := ansi.Cut(bgLines[y], 0, pad)
		right := ansi.Cut(bgLines[y], pad+fw, width)
		bgLines[y] = left + fl + right
	}
	return strings.Join(bgLines, "\n")
}
