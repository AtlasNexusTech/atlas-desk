// Atlas Desk Agent — H.264 video encode via FFmpeg hardware-accelerated pipe
// Uses NVENC/VAAPI when available, falls back to libx264 software.
package main

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// H264Encoder wraps an FFmpeg subprocess for hardware-accelerated H.264 encoding.
// Input: raw RGBA frames via stdin pipe. Output: H.264 Annex B NAL units via stdout pipe.
type H264Encoder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	width  int
	height int
	fps    int
	mu     sync.Mutex
}

// NewH264Encoder starts an FFmpeg process. Tries hardware encoder first, falls back to software.
func NewH264Encoder(width, height, fps int) (*H264Encoder, error) {
	// Try hardware-accelerated encoder first (VAAPI on Linux, NVENC on NVIDIA)
	var args []string
	if hwEncAvailable("h264_vaapi") {
		args = []string{
			"-f", "rawvideo",
			"-pix_fmt", "rgba",
			"-s", fmt.Sprintf("%dx%d", width, height),
			"-r", fmt.Sprintf("%d", fps),
			"-i", "pipe:0",
			"-c:v", "h264_vaapi",
			"-vaapi_device", "/dev/dri/renderD128",
			"-b:v", "2M",
			"-maxrate", "4M",
			"-bufsize", "4M",
			"-g", fmt.Sprintf("%d", fps*2), // keyframe every 2 seconds
			"-f", "h264",
			"-an", // no audio
			"pipe:1",
		}
		log.Printf("🎥 H.264: VAAPI hardware encoder")
	} else if hwEncAvailable("h264_nvenc") {
		args = []string{
			"-f", "rawvideo",
			"-pix_fmt", "rgba",
			"-s", fmt.Sprintf("%dx%d", width, height),
			"-r", fmt.Sprintf("%d", fps),
			"-i", "pipe:0",
			"-c:v", "h264_nvenc",
			"-preset", "p1", // fastest
			"-tune", "ll", // low latency
			"-b:v", "2M",
			"-maxrate", "4M",
			"-bufsize", "4M",
			"-g", fmt.Sprintf("%d", fps*2),
			"-f", "h264",
			"-an",
			"pipe:1",
		}
		log.Printf("🎥 H.264: NVENC hardware encoder")
	} else {
		args = []string{
			"-f", "rawvideo",
			"-pix_fmt", "rgba",
			"-s", fmt.Sprintf("%dx%d", width, height),
			"-r", fmt.Sprintf("%d", fps),
			"-i", "pipe:0",
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-b:v", "2M",
			"-maxrate", "4M",
			"-bufsize", "4M",
			"-g", fmt.Sprintf("%d", fps*2),
			"-f", "h264",
			"-an",
			"pipe:1",
		}
		log.Printf("🎥 H.264: libx264 software encoder (no hardware encoder found)")
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = nil // suppress FFmpeg debug output

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("FFmpeg start failed: %w (is ffmpeg installed?)", err)
	}

	return &H264Encoder{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		width:  width,
		height: height,
		fps:    fps,
	}, nil
}

// Encode sends an RGBA frame to FFmpeg. Returns immediately (async pipe write).
func (e *H264Encoder) Encode(img image.Image) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	bounds := img.Bounds()
	raw := make([]byte, bounds.Dx()*bounds.Dy()*4)
	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			raw[idx] = byte(r >> 8)
			raw[idx+1] = byte(g >> 8)
			raw[idx+2] = byte(b >> 8)
			raw[idx+3] = byte(a >> 8)
			idx += 4
		}
	}
	_, err := e.stdin.Write(raw)
	return err
}

// ReadNALUnit reads a single H.264 NAL unit from FFmpeg's stdout.
// Returns the raw NAL unit bytes (including start code 00 00 00 01 or 00 00 01).
func (e *H264Encoder) ReadNALUnit() ([]byte, error) {
	// Read until we find a start code prefix
	var buf bytes.Buffer
	tmp := make([]byte, 4096)

	for {
		n, err := e.stdout.Read(tmp)
		if err != nil {
			if err == io.EOF {
				if buf.Len() > 0 {
					return buf.Bytes(), nil
				}
				return nil, err
			}
			return nil, err
		}
		buf.Write(tmp[:n])

		// Split on start code boundaries (00 00 00 01 or 00 00 01)
		data := buf.Bytes()
		for i := 0; i < len(data)-4; i++ {
			if (data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1) ||
				(data[i] == 0 && data[i+1] == 0 && data[i+2] == 1) {
				// Found a start code — return bytes up to (but not including) this start code
				// unless it's at position 0 (first NAL unit)
				if i > 0 {
					nal := make([]byte, i)
					copy(nal, data[:i])
					// Consume returned bytes by resetting buffer with remainder
					remaining := make([]byte, len(data)-i)
					copy(remaining, data[i:])
					buf.Reset()
					buf.Write(remaining)
					return nal, nil
				}
			}
		}
		// No complete NAL unit yet — keep reading
	}
}

// WriteSampleToTrack reads an H.264 NAL unit and writes it as an RTP sample.
func (e *H264Encoder) WriteSampleToTrack(track *webrtc.TrackLocalStaticSample) error {
	nal, err := e.ReadNALUnit()
	if err != nil {
		return err
	}
	if len(nal) == 0 {
		return nil
	}
	return track.WriteSample(media.Sample{
		Data:     nal,
		Duration: 0, // let pion calculate
	})
}

// StreamToTrack continuously reads H.264 NAL units and writes them to the video track.
// Runs in a goroutine. Returns on error or when stopCh is closed.
func (e *H264Encoder) StreamToTrack(track *webrtc.TrackLocalStaticSample, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		nal, err := e.ReadNALUnit()
		if err != nil {
			if err != io.EOF {
				log.Printf("H.264 read error: %v", err)
			}
			return
		}
		if len(nal) > 0 {
			if err := track.WriteSample(media.Sample{Data: nal, Duration: 0}); err != nil {
				log.Printf("H.264 write error: %v", err)
				return
			}
		}
	}
}

// Close terminates the FFmpeg process.
func (e *H264Encoder) Close() {
	e.stdin.Close()
	e.cmd.Wait()
}

// hwEncAvailable checks if an FFmpeg encoder is available.
func hwEncAvailable(name string) bool {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-encoders")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte(name))
}
