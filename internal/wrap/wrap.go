// Package wrap word-wraps terminal text.
package wrap

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Hang writes text wrapped to width, with prefix on the first line (e.g. a
// bullet) and continuation lines indented to align after it. ANSI color codes
// don't count toward line length.
func Hang(w io.Writer, prefix, text string, indent, width int) {
	pad := strings.Repeat(" ", indent)
	cont := pad + strings.Repeat(" ", visibleLen(prefix))
	line, col := pad+prefix, len(cont)
	start := col
	for word := range strings.FieldsSeq(text) {
		n := visibleLen(word)
		if col > start && col+1+n > width {
			fmt.Fprintln(w, line)
			line, col = cont, len(cont)
		} else if col > start {
			line += " "
			col++
		}
		line += word
		col += n
	}
	fmt.Fprintln(w, line)
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleLen(s string) int { return len([]rune(ansi.ReplaceAllString(s, ""))) }

// Columns writes items in column-major order like ls, using as many columns
// as fit in width, each as wide as its longest item.
func Columns(w io.Writer, items []string, indent, width int) {
	const gap = 3
	pad := strings.Repeat(" ", indent)
	lens := make([]int, len(items))
	for i, it := range items {
		lens[i] = visibleLen(it)
	}
	var widths []int
	rows := len(items)
	for cols := len(items); cols > 1; cols-- {
		r := (len(items) + cols - 1) / cols
		if ws, ok := colWidths(lens, r, width-indent, gap); ok {
			rows, widths = r, ws
			break
		}
	}
	for r := range rows {
		line := pad
		for c := 0; r+c*rows < len(items); c++ {
			i := r + c*rows
			line += items[i]
			if next := i + rows; next < len(items) {
				line += strings.Repeat(" ", widths[c]-lens[i]+gap)
			}
		}
		fmt.Fprintln(w, line)
	}
}

// colWidths returns per-column widths for items laid out in rows, and whether
// they fit in width.
func colWidths(lens []int, rows, width, gap int) ([]int, bool) {
	var widths []int
	total := -gap
	for start := 0; start < len(lens); start += rows {
		cw := slices.Max(lens[start:min(start+rows, len(lens))])
		widths = append(widths, cw)
		if total += cw + gap; total > width {
			return nil, false
		}
	}
	return widths, true
}
