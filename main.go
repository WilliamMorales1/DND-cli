// Command dnd is a terminal D&D character tracker: dice, checks, attacks,
// resources, and inventory, with state saved after every change.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"dnd/internal/lineedit"
)

var stdout io.Writer = os.Stdout

func main() {
	s := &Session{Chars: Roster(), SavePath: savePath(), Library: openLibrary()}
	var err error
	if s.Current, err = Load(s.SavePath, s.Chars); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read %s (%v), starting from character sheets\n", s.SavePath, err)
	}

	ed := lineedit.New()
	ed.Complete = s.Complete

	fmt.Println("D&D CLI — type 'help' for commands.")
	for {
		prompt := fmt.Sprintf("[%s %d/%d] > ", s.Current.Name, s.Current.HP, s.Current.MaxHP)
		line, err := ed.ReadLine(prompt)
		if err != nil {
			if !ed.IsTTY() {
				fmt.Println()
			}
			if !errors.Is(err, io.EOF) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		if errors.Is(s.Exec(line), errQuit) {
			return
		}
	}
}
