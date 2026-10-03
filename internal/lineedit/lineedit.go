// Package lineedit is a minimal fish-style line editor: cursor movement,
// history, a dim inline suggestion accepted with Right/End, and Tab completion.
// When stdin is not a terminal it falls back to plain buffered reads.
package lineedit

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// Completer returns full-line candidates that extend line.
type Completer func(line string) []string

type Editor struct {
	Complete Completer

	history []string
	in      *bufio.Reader
	out     io.Writer
	fd      int
	isTTY   bool
}

func New() *Editor {
	fd := int(os.Stdin.Fd())
	return &Editor{
		in:    bufio.NewReader(os.Stdin),
		out:   os.Stdout,
		fd:    fd,
		isTTY: term.IsTerminal(fd),
	}
}

func (e *Editor) IsTTY() bool { return e.isTTY }

// ReadLine prints prompt and returns the entered line, or io.EOF.
func (e *Editor) ReadLine(prompt string) (string, error) {
	fmt.Fprint(e.out, prompt)
	if !e.isTTY {
		line, err := e.in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	// Raw mode only while editing, so command output and Ctrl-Z behave normally.
	state, err := term.MakeRaw(e.fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(e.fd, state)

	s := session{Editor: e, prompt: prompt, histIdx: len(e.history)}
	return s.run()
}

type session struct {
	*Editor
	prompt  string
	buf     []rune
	cursor  int
	ghost   string // suggested text past the end of the buffer
	draft   string // unsubmitted line, kept while browsing history
	histIdx int
}

func (s *session) run() (string, error) {
	for {
		r, _, err := s.in.ReadRune()
		if err != nil {
			return "", err
		}
		switch r {
		case '\r', '\n':
			// Raw mode disables output post-processing, so emit CR explicitly.
			fmt.Fprint(s.out, "\r\n")
			line := string(s.buf)
			s.remember(line)
			return line, nil
		case 3: // Ctrl-C: abandon line
			fmt.Fprint(s.out, "^C\r\n")
			return "", nil
		case 4: // Ctrl-D: EOF on empty line
			if len(s.buf) == 0 {
				fmt.Fprint(s.out, "\r\n")
				return "", io.EOF
			}
		case 127, 8: // Backspace
			if s.cursor > 0 {
				s.buf = slices.Delete(s.buf, s.cursor-1, s.cursor)
				s.cursor--
			}
		case '\t':
			s.tab()
		case 0x1b:
			s.escape()
		default:
			if r < 0x20 || r == utf8.RuneError {
				continue
			}
			s.buf = slices.Insert(s.buf, s.cursor, r)
			s.cursor++
		}
		s.redraw()
	}
}

func (s *session) remember(line string) {
	line = strings.TrimSpace(line)
	if line != "" && (len(s.history) == 0 || s.history[len(s.history)-1] != line) {
		s.history = append(s.history, line)
	}
}

func (s *session) load(line string) {
	s.buf = []rune(line)
	s.cursor = len(s.buf)
}

func (s *session) accept() {
	s.load(string(s.buf) + s.ghost)
}

// tab extends to the longest common prefix of candidates, or lists them.
func (s *session) tab() {
	if s.cursor != len(s.buf) || s.Complete == nil {
		return
	}
	line := string(s.buf)
	cands := s.Complete(line)
	if len(cands) == 0 {
		return
	}
	prefix := cands[0]
	for _, c := range cands[1:] {
		prefix = prefix[:commonPrefix(prefix, c)]
	}
	switch {
	case len(prefix) > len(line):
		s.load(prefix)
	case len(cands) > 1:
		start := strings.LastIndexByte(line, ' ') + 1
		fmt.Fprint(s.out, "\r\n")
		for _, c := range cands {
			fmt.Fprintf(s.out, "%s  ", c[start:])
		}
		fmt.Fprint(s.out, "\r\n")
	}
}

// escape consumes a CSI ("ESC [ A") or SS3 ("ESC O A") sequence and applies it.
func (s *session) escape() {
	intro, err := s.in.ReadByte()
	if err != nil || (intro != '[' && intro != 'O') {
		return
	}
	var final byte
	for {
		b, err := s.in.ReadByte()
		if err != nil {
			return
		}
		if b >= 0x40 && b <= 0x7e {
			final = b
			break
		}
	}
	switch final {
	case 'D': // Left
		s.cursor = max(s.cursor-1, 0)
	case 'C': // Right
		if s.cursor == len(s.buf) {
			s.accept()
		} else {
			s.cursor++
		}
	case 'H': // Home
		s.cursor = 0
	case 'F': // End
		s.accept()
	case 'A': // Up
		if s.histIdx > 0 {
			if s.histIdx == len(s.history) {
				s.draft = string(s.buf)
			}
			s.histIdx--
			s.load(s.history[s.histIdx])
		}
	case 'B': // Down
		if s.histIdx < len(s.history) {
			s.histIdx++
			if s.histIdx == len(s.history) {
				s.load(s.draft)
			} else {
				s.load(s.history[s.histIdx])
			}
		}
	}
}

// redraw prints prompt + buffer + dim suggestion, then moves the cursor back into place.
func (s *session) redraw() {
	line := string(s.buf)
	s.ghost = ""
	if s.cursor == len(s.buf) {
		s.ghost = s.suggest(line)
	}
	fmt.Fprintf(s.out, "\r%s%s\x1b[K", s.prompt, line)
	if s.ghost != "" {
		fmt.Fprintf(s.out, "\x1b[90m%s\x1b[0m\x1b[%dD", s.ghost, utf8.RuneCountInString(s.ghost))
	}
	if tail := len(s.buf) - s.cursor; tail > 0 {
		fmt.Fprintf(s.out, "\x1b[%dD", tail)
	}
}

// suggest prefers the most recent history entry extending line, then the first completion.
func (s *session) suggest(line string) string {
	if strings.TrimSpace(line) == "" {
		return ""
	}
	for _, h := range slices.Backward(s.history) {
		if rest, ok := strings.CutPrefix(h, line); ok && rest != "" {
			return rest
		}
	}
	if s.Complete != nil {
		for _, c := range s.Complete(line) {
			if rest, ok := strings.CutPrefix(c, line); ok && rest != "" {
				return rest
			}
		}
	}
	return ""
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
