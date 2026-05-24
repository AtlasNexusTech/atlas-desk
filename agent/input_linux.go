//go:build !windows

package main

import (
	"fmt"
	"os/exec"
)

func moveMouse(x, y int) {
	exec.Command("xdotool", "mousemove", fmt.Sprint(x), fmt.Sprint(y)).Run()
}

func clickMouse(btn string) {
	b := "1"
	if btn == "right" { b = "3" }
	exec.Command("xdotool", "click", b).Run()
}

func pressKey(key string) {
	exec.Command("xdotool", "key", key).Run()
}

func typeText(text string) {
	exec.Command("xdotool", "type", text).Run()
}

func scrollMouse(dy int) {
	btn := "4"
	for i := 0; i < abs(dy); i++ { exec.Command("xdotool", "click", btn).Run() }
}

func abs(n int) int { if n < 0 { return -n }; return n }
