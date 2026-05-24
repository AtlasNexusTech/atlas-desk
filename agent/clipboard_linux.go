//go:build !windows

package main

import (
	"os/exec"
	"strings"
)

type linuxClipboard struct{}

func newClipboard() ClipboardManager {
	return &linuxClipboard{}
}

func (c *linuxClipboard) GetText() string {
	out, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (c *linuxClipboard) SetText(text string) error {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-i")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
