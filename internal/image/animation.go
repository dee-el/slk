package image

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"
	"time"
)

const (
	animationMaxSourceBytes = 8 << 20
	animationMaxCanvasSize  = 512
	animationMaxFrames      = 256
	animationMinDelay       = 20 * time.Millisecond
	animationDelayFloor     = 100 * time.Millisecond
)

// Animation is a fully-composited frame sequence suitable for stable-ID
// kitty frame replacement.
type Animation struct {
	Frames    []*image.RGBA
	Delays    []time.Duration
	LoopCount int
	Duration  time.Duration
}

// FrameIndexAt selects the GIF frame for elapsed wall time. Finite-loop GIFs
// freeze on their final frame once all loops complete.
func (a *Animation) FrameIndexAt(elapsed time.Duration) int {
	if a == nil || len(a.Frames) == 0 || len(a.Delays) == 0 {
		return 0
	}
	if len(a.Frames) == 1 || a.Duration <= 0 {
		return 0
	}
	if elapsed < 0 {
		elapsed = 0
	}
	loops := 0
	switch {
	case a.LoopCount == 0:
		loops = 0
	case a.LoopCount < 0:
		loops = 1
	default:
		loops = a.LoopCount + 1
	}
	if loops > 0 {
		total := time.Duration(loops) * a.Duration
		if elapsed >= total {
			return len(a.Frames) - 1
		}
	}
	if a.Duration > 0 {
		elapsed %= a.Duration
	}
	for i, d := range a.Delays {
		if elapsed < d {
			return i
		}
		elapsed -= d
	}
	return len(a.Frames) - 1
}

func decodeGIFAnimation(data []byte) (*Animation, error) {
	if len(data) == 0 {
		return nil, errors.New("empty gif")
	}
	if len(data) > animationMaxSourceBytes {
		return nil, fmt.Errorf("gif too large: %d bytes > %d", len(data), animationMaxSourceBytes)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(g.Image) == 0 {
		return nil, errors.New("gif has no frames")
	}
	if len(g.Image) > animationMaxFrames {
		return nil, fmt.Errorf("gif has too many frames: %d > %d", len(g.Image), animationMaxFrames)
	}
	if g.Config.Width <= 0 || g.Config.Height <= 0 {
		return nil, fmt.Errorf("gif has invalid canvas: %dx%d", g.Config.Width, g.Config.Height)
	}
	if g.Config.Width > animationMaxCanvasSize || g.Config.Height > animationMaxCanvasSize {
		return nil, fmt.Errorf("gif canvas too large: %dx%d > %dx%d", g.Config.Width, g.Config.Height, animationMaxCanvasSize, animationMaxCanvasSize)
	}

	canvasRect := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	canvas := image.NewRGBA(canvasRect)
	frames := make([]*image.RGBA, 0, len(g.Image))
	delays := make([]time.Duration, 0, len(g.Image))
	var duration time.Duration
	var snapshot *image.RGBA

	for i, frame := range g.Image {
		if i > 0 {
			switch disposalAt(g, i-1) {
			case gif.DisposalBackground:
				clearRGBARect(canvas, g.Image[i-1].Bounds())
			case gif.DisposalPrevious:
				if snapshot != nil {
					stddraw.Draw(canvas, canvas.Bounds(), snapshot, snapshot.Bounds().Min, stddraw.Src)
				}
			}
		}

		if disposalAt(g, i) == gif.DisposalPrevious {
			snapshot = cloneRGBA(canvas)
		} else {
			snapshot = nil
		}

		stddraw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, stddraw.Over)
		frames = append(frames, cloneRGBA(canvas))

		delay := time.Duration(delayAt(g, i)) * 10 * time.Millisecond
		if delay < animationMinDelay {
			delay = animationDelayFloor
		}
		delays = append(delays, delay)
		duration += delay
	}

	return &Animation{
		Frames:    frames,
		Delays:    delays,
		LoopCount: g.LoopCount,
		Duration:  duration,
	}, nil
}

func disposalAt(g *gif.GIF, idx int) byte {
	if idx < 0 || idx >= len(g.Disposal) {
		return gif.DisposalNone
	}
	return g.Disposal[idx]
}

func delayAt(g *gif.GIF, idx int) int {
	if idx < 0 || idx >= len(g.Delay) {
		return 0
	}
	return g.Delay[idx]
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	dst := image.NewRGBA(src.Bounds())
	stddraw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, stddraw.Src)
	return dst
}

func clearRGBARect(dst *image.RGBA, rect image.Rectangle) {
	if dst == nil {
		return
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if image.Pt(x, y).In(dst.Bounds()) {
				dst.SetRGBA(x, y, color.RGBA{})
			}
		}
	}
}
