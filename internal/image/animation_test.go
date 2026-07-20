package image

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"
)

func TestDecodeGIFAnimation_DisposalBackground(t *testing.T) {
	pal := color.Palette{
		color.RGBA{0, 0, 0, 0},
		color.RGBA{255, 0, 0, 255},
		color.RGBA{0, 0, 255, 255},
	}
	frame0 := image.NewPaletted(image.Rect(0, 0, 4, 2), pal)
	fillPaletted(frame0, 1)
	frame1 := image.NewPaletted(image.Rect(0, 0, 1, 1), pal)
	fillPaletted(frame1, 2)

	anim, err := decodeGIFAnimation(encodeGIF(t, &gif.GIF{
		Image:     []*image.Paletted{frame0, frame1},
		Delay:     []int{5, 5},
		Disposal:  []byte{gif.DisposalBackground, gif.DisposalNone},
		LoopCount: 0,
		Config:    image.Config{Width: 4, Height: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := rgbaAtTest(anim.Frames[0], 3, 1); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("frame0 pixel = %#v, want solid red", got)
	}
	if got := rgbaAtTest(anim.Frames[1], 0, 0); got != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("frame1 origin = %#v, want blue", got)
	}
	if got := rgbaAtTest(anim.Frames[1], 3, 1); got.A != 0 {
		t.Fatalf("frame1 background alpha = %d, want 0 after DisposalBackground", got.A)
	}
}

func TestDecodeGIFAnimation_DisposalPrevious(t *testing.T) {
	pal := color.Palette{
		color.RGBA{0, 0, 0, 0},
		color.RGBA{255, 0, 0, 255},
		color.RGBA{0, 0, 255, 255},
		color.RGBA{0, 255, 0, 255},
	}
	frame0 := image.NewPaletted(image.Rect(0, 0, 4, 1), pal)
	fillPaletted(frame0, 1)
	frame1 := image.NewPaletted(image.Rect(1, 0, 2, 1), pal)
	fillPaletted(frame1, 2)
	frame2 := image.NewPaletted(image.Rect(0, 0, 1, 1), pal)
	fillPaletted(frame2, 3)

	anim, err := decodeGIFAnimation(encodeGIF(t, &gif.GIF{
		Image:     []*image.Paletted{frame0, frame1, frame2},
		Delay:     []int{5, 5, 5},
		Disposal:  []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
		LoopCount: 0,
		Config:    image.Config{Width: 4, Height: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := rgbaAtTest(anim.Frames[1], 1, 0); got != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("frame1 pixel = %#v, want blue overlay", got)
	}
	if got := rgbaAtTest(anim.Frames[2], 0, 0); got != (color.RGBA{0, 255, 0, 255}) {
		t.Fatalf("frame2 origin = %#v, want green", got)
	}
	if got := rgbaAtTest(anim.Frames[2], 1, 0); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("frame2 restored pixel = %#v, want red after DisposalPrevious restore", got)
	}
}

func TestDecodeGIFAnimation_DelayClampAndInfiniteLoop(t *testing.T) {
	anim, err := decodeGIFAnimation(twoFrameGIF(t, 0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := anim.Delays[0], 100*time.Millisecond; got != want {
		t.Fatalf("frame0 delay = %s, want %s", got, want)
	}
	if got, want := anim.Delays[1], 100*time.Millisecond; got != want {
		t.Fatalf("frame1 delay = %s, want %s", got, want)
	}
	if got := anim.FrameIndexAt(0); got != 0 {
		t.Fatalf("frame at t=0 = %d, want 0", got)
	}
	if got := anim.FrameIndexAt(100 * time.Millisecond); got != 1 {
		t.Fatalf("frame at t=100ms = %d, want 1", got)
	}
	if got := anim.FrameIndexAt(250 * time.Millisecond); got != 0 {
		t.Fatalf("frame at t=250ms = %d, want 0 after wrap", got)
	}
}

func TestDecodeGIFAnimation_FiniteLoopFreezesLastFrame(t *testing.T) {
	anim, err := decodeGIFAnimation(twoFrameGIF(t, 5, 5, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := anim.FrameIndexAt(49 * time.Millisecond); got != 0 {
		t.Fatalf("frame at t=49ms = %d, want 0", got)
	}
	if got := anim.FrameIndexAt(150 * time.Millisecond); got != 1 {
		t.Fatalf("frame at t=150ms = %d, want 1", got)
	}
	if got := anim.FrameIndexAt(500 * time.Millisecond); got != 1 {
		t.Fatalf("frame after loop end = %d, want final frame frozen", got)
	}
}

func TestDecodeGIFAnimation_Limits(t *testing.T) {
	t.Run("source size", func(t *testing.T) {
		if _, err := decodeGIFAnimation(bytes.Repeat([]byte("x"), animationMaxSourceBytes+1)); err == nil {
			t.Fatal("expected size-limit error")
		}
	})

	t.Run("frame count", func(t *testing.T) {
		pal := color.Palette{color.RGBA{0, 0, 0, 0}, color.RGBA{255, 0, 0, 255}}
		g := &gif.GIF{Config: image.Config{Width: 1, Height: 1}}
		for i := 0; i < animationMaxFrames+1; i++ {
			f := image.NewPaletted(image.Rect(0, 0, 1, 1), pal)
			fillPaletted(f, 1)
			g.Image = append(g.Image, f)
			g.Delay = append(g.Delay, 5)
		}
		if _, err := decodeGIFAnimation(encodeGIF(t, g)); err == nil {
			t.Fatal("expected frame-limit error")
		}
	})

	t.Run("canvas size", func(t *testing.T) {
		pal := color.Palette{color.RGBA{0, 0, 0, 0}, color.RGBA{255, 0, 0, 255}}
		f := image.NewPaletted(image.Rect(0, 0, animationMaxCanvasSize+1, 1), pal)
		fillPaletted(f, 1)
		if _, err := decodeGIFAnimation(encodeGIF(t, &gif.GIF{
			Image:  []*image.Paletted{f},
			Delay:  []int{5},
			Config: image.Config{Width: animationMaxCanvasSize + 1, Height: 1},
		})); err == nil {
			t.Fatal("expected canvas-limit error")
		}
	})
}

func twoFrameGIF(t testing.TB, delay0, delay1, loopCount int) []byte {
	t.Helper()
	pal := color.Palette{
		color.RGBA{0, 0, 0, 0},
		color.RGBA{255, 0, 0, 255},
		color.RGBA{0, 0, 255, 255},
	}
	frame0 := image.NewPaletted(image.Rect(0, 0, 1, 1), pal)
	fillPaletted(frame0, 1)
	frame1 := image.NewPaletted(image.Rect(0, 0, 1, 1), pal)
	fillPaletted(frame1, 2)
	return encodeGIF(t, &gif.GIF{
		Image:     []*image.Paletted{frame0, frame1},
		Delay:     []int{delay0, delay1},
		LoopCount: loopCount,
		Config:    image.Config{Width: 1, Height: 1},
	})
}

func encodeGIF(t testing.TB, g *gif.GIF) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fillPaletted(img *image.Paletted, idx uint8) {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetColorIndex(x, y, idx)
		}
	}
}

func rgbaAtTest(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
