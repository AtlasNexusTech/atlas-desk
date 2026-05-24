// Atlas Desk Agent — Screen diff encoding
// Only sends changed 32×32 blocks instead of full frames.
// Bandwidth reduction: 50-80% for typical desktop use.

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"log"
)

const BlockSize = 32 // 32×32 pixel blocks

// DiffEncoder tracks previous frame to compute diffs.
type DiffEncoder struct {
	prev     *image.RGBA // previous frame pixels
	quality  int         // JPEG quality
	fullFreq uint64      // send full frame every N frames (for recovery)
}

func NewDiffEncoder(quality int) *DiffEncoder {
	return &DiffEncoder{
		quality:  quality,
		fullFreq: 30, // full keyframe every 30 frames (~2 seconds at 15 FPS)
	}
}

// EncodeDiff compares img against the previous frame and returns a diff packet.
// If prev is nil or fullFreq triggers, returns a full frame.
// Packet format:
//
//	Full:  [0x00][4B metaLen][JSON meta][4B jpgLen][JPEG]
//	Diff:  [0x01][4B metaLen][JSON meta][2B numBlocks][block1...]
//	Block: [2B x][2B y][2B w][2B h][4B jpgLen][JPEG data]
func (d *DiffEncoder) EncodeDiff(img image.Image, bounds image.Rectangle, frameNum uint64) []byte {
	w, h := bounds.Dx(), bounds.Dy()
	meta := FrameMeta{W: w, H: h, Frame: frameNum}
	metaJSON, _ := json.Marshal(meta)

	fullFrame := d.prev == nil || frameNum%d.fullFreq == 0

	if fullFrame {
		// Keyframe — send whole image
		d.prev = imageToRGBA(img, bounds)
		var jpgBuf bytes.Buffer
		jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: d.quality})

		var pkt bytes.Buffer
		pkt.WriteByte(0x00) // full frame
		binary.Write(&pkt, binary.BigEndian, uint32(len(metaJSON)))
		pkt.Write(metaJSON)
		binary.Write(&pkt, binary.BigEndian, uint32(jpgBuf.Len()))
		pkt.Write(jpgBuf.Bytes())
		return pkt.Bytes()
	}

	// Diff frame — find changed blocks
	currentRGBA := imageToRGBA(img, bounds)
	blocks := findChangedBlocks(d.prev, currentRGBA, w, h)

	if len(blocks) == 0 {
		// Nothing changed — send empty diff
		var pkt bytes.Buffer
		pkt.WriteByte(0x01) // diff frame
		binary.Write(&pkt, binary.BigEndian, uint32(len(metaJSON)))
		pkt.Write(metaJSON)
		binary.Write(&pkt, binary.BigEndian, uint16(0)) // zero blocks
		d.prev = currentRGBA
		return pkt.Bytes()
	}

	// Limit blocks to avoid blowing up the data channel
	maxBlocks := 60
	if len(blocks) > maxBlocks {
		blocks = blocks[:maxBlocks]
	}

	// Encode each changed block as JPEG
	type encodedBlock struct {
		x, y, w, h uint16
		data       []byte
	}
	var encoded []encodedBlock

	for _, b := range blocks {
		subImg := currentRGBA.SubImage(image.Rect(b.x, b.y, b.x+b.w, b.y+b.h))
		var jpgBuf bytes.Buffer
		jpeg.Encode(&jpgBuf, subImg, &jpeg.Options{Quality: d.quality})
		encoded = append(encoded, encodedBlock{
			x: uint16(b.x), y: uint16(b.y),
			w: uint16(b.w), h: uint16(b.h),
			data: jpgBuf.Bytes(),
		})
	}

	// Build diff packet
	var pkt bytes.Buffer
	pkt.WriteByte(0x01) // diff frame
	binary.Write(&pkt, binary.BigEndian, uint32(len(metaJSON)))
	pkt.Write(metaJSON)
	binary.Write(&pkt, binary.BigEndian, uint16(len(encoded))) // numBlocks

	for _, b := range encoded {
		binary.Write(&pkt, binary.BigEndian, b.x)
		binary.Write(&pkt, binary.BigEndian, b.y)
		binary.Write(&pkt, binary.BigEndian, b.w)
		binary.Write(&pkt, binary.BigEndian, b.h)
		binary.Write(&pkt, binary.BigEndian, uint32(len(b.data)))
		pkt.Write(b.data)
	}

	d.prev = currentRGBA

	// Log bandwidth savings
	fullSize := estimateFullSize(w, h, d.quality)
	saved := 100 - (len(pkt.Bytes())*100)/max(fullSize, 1)
	if frameNum%60 == 0 {
		log.Printf("📊 Diff: %d blocks, %d bytes (est. ~%d%% saved vs full frame)", len(blocks), pkt.Len(), saved)
	}

	return pkt.Bytes()
}

type block struct {
	x, y, w, h int
}

// findChangedBlocks compares two RGBA images in 32×32 blocks.
func findChangedBlocks(prev, curr *image.RGBA, imgW, imgH int) []block {
	var blocks []block

	for by := 0; by < imgH; by += BlockSize {
		for bx := 0; bx < imgW; bx += BlockSize {
			bw := min(BlockSize, imgW-bx)
			bh := min(BlockSize, imgH-by)

			if blockChanged(prev, curr, bx, by, bw, bh, imgW) {
				blocks = append(blocks, block{bx, by, bw, bh})
			}
		}
	}
	return blocks
}

// blockChanged checks if any pixel in the block differs.
func blockChanged(prev, curr *image.RGBA, x, y, w, h, stride int) bool {
	for dy := 0; dy < h; dy++ {
		off := ((y+dy)*stride + x) * 4
		pRow := prev.Pix[off : off+w*4]
		cRow := curr.Pix[off : off+w*4]
		if !bytes.Equal(pRow, cRow) {
			return true
		}
	}
	return false
}

// imageToRGBA converts any image to RGBA for pixel comparison.
func imageToRGBA(img image.Image, bounds image.Rectangle) *image.RGBA {
	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}
	return rgba
}

// estimateFullSize estimates the size of a full-frame JPEG.
func estimateFullSize(w, h, quality int) int {
	return (w * h * quality) / 200
}
