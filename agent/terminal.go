// Atlas Desk — Terminal (PTY shell) support
// Data channel "terminal" : client → agent (input) / agent → client (output)
// Uses creack/pty for a real PTY (bash/powershell/cmd with full interactivity).
package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/creack/pty"
	"github.com/pion/webrtc/v4"
)

var (
	termMu    sync.Mutex
	termCmd   *exec.Cmd
	termPTY   *os.File
	termDC    *webrtc.DataChannel
	termReady = false
)

// spawnTerminal starts a PTY shell on the target machine.
func spawnTerminal(dc *webrtc.DataChannel) error {
	termMu.Lock()
	defer termMu.Unlock()

	// If a terminal is already running, restart it (fresh shell)
	if termCmd != nil && termPTY != nil {
		_ = termCmd.Process.Kill()
		_ = termPTY.Close()
		termCmd = nil
		termPTY = nil
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe")
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		cmd = exec.Command(shell)
	}
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("⚠ Terminal PTY error: %v", err)
		return err
	}

	termCmd = cmd
	termPTY = ptmx
	termDC = dc
	termReady = true

	// Stream PTY output → data channel (line-buffered chunks)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				payload := map[string]interface{}{
					"type": "terminal_output",
					"data": string(buf[:n]),
				}
				data, _ := json.Marshal(payload)
				if err := dc.Send([]byte(data)); err != nil {
					log.Printf("⚠ Terminal send error: %v", err)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Wait for shell exit
	go func() {
		_ = cmd.Wait()
		termMu.Lock()
		termCmd = nil
		termPTY = nil
		termReady = false
		termMu.Unlock()
		log.Println("⏹ Terminal shell exited")
	}()

	log.Println("🖥 Terminal ready")
	return nil
}

// handleTerminalMessage processes incoming terminal messages from the client.
func handleTerminalMessage(msg webrtc.DataChannelMessage) {
	var meta map[string]interface{}
	if err := json.Unmarshal(msg.Data, &meta); err != nil {
		return
	}
	msgType, _ := meta["type"].(string)

	switch msgType {
	case "terminal_start":
		// Client requests a (re)start of the shell
		if termDC != nil {
			_ = spawnTerminal(termDC)
		}
	case "terminal_input":
		data, _ := meta["data"].(string)
		termMu.Lock()
		if termPTY != nil {
			_, _ = termPTY.Write([]byte(data))
		}
		termMu.Unlock()
	case "terminal_resize":
		rows, _ := meta["rows"].(float64)
		cols, _ := meta["cols"].(float64)
		termMu.Lock()
		if termPTY != nil {
			_ = pty.Setsize(termPTY, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
		}
		termMu.Unlock()
	case "terminal_stop":
		termMu.Lock()
		if termCmd != nil {
			_ = termCmd.Process.Kill()
		}
		if termPTY != nil {
			_ = termPTY.Close()
		}
		termCmd = nil
		termPTY = nil
		termReady = false
		termMu.Unlock()
	}
}
