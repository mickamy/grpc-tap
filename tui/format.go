package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
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
