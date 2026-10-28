package app

import (
	"os"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDumpFrames(t *testing.T) {
	dir := os.Getenv("DUMP_DIR")
	if dir == "" {
		t.Skip("set DUMP_DIR to write frames")
	}
	d := newDriver(t, 120, 36, Options{ServerURL: "https://hs.example.com"})
	write := func(name string) { os.WriteFile(dir+"/"+name+".txt", []byte(ansi.Strip(d.view())), 0o644) }
	write("1-dashboard")
	d.key("2")
	write("2-machines")
	d.key("j", "j", "j")
	write("3-machines-cursor")
	d.key("3")
	write("4-pending")
	d.key("n")
	write("5-newkey-form")
	d.key("esc", "2", "D")
	write("6-delete-confirm")
}
