//go:build windows || (linux && cgo && desktop)

package main

import (
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

var (
	ink   = rgb(0xf4f8ff)
	muted = rgb(0x8fa5c4)
	dim   = rgb(0x58708f)
	line  = rgba(0x7ba6e1, 18)
	blue  = rgb(0x67b2ff)
	green = rgb(0x54dca2)
)

func rgb(hex uint32) color.NRGBA { return rgba(hex, 255) }

func rgba(hex uint32, alpha uint8) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: alpha}
}

func desktopTheme() *material.Theme {
	theme := material.NewTheme()
	faces := gofont.Collection()
	if runtime.GOOS == "windows" {
		for _, name := range []string{"segoeui.ttf", "seguisb.ttf", "segoeuib.ttf", "consola.ttf"} {
			data, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "Fonts", name))
			if err == nil {
				if found, err := opentype.ParseCollection(data); err == nil {
					faces = append(faces, found...)
				}
			}
		}
		theme.Face = "Segoe UI"
	}
	theme.Shaper = text.NewShaper(text.WithCollection(faces))
	theme.Fg = ink
	theme.Bg = rgb(0x050a12)
	return theme
}

func dp(gtx layout.Context, n int) int { return gtx.Dp(unit.Dp(n)) }

func at(gtx layout.Context, x, y int, fn func()) {
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	fn()
}

func rect(gtx layout.Context, x, y, w, h int) image.Rectangle {
	return image.Rect(dp(gtx, x), dp(gtx, y), dp(gtx, x+w), dp(gtx, y+h))
}

func box(gtx layout.Context, bounds image.Rectangle, radius int, fill, border color.NRGBA) {
	if bounds.Empty() {
		return
	}
	shape := clip.UniformRRect(bounds, dp(gtx, radius))
	if fill.A != 0 {
		paint.FillShape(gtx.Ops, fill, shape.Op(gtx.Ops))
	}
	if border.A != 0 {
		paint.FillShape(gtx.Ops, border, clip.Stroke{Path: shape.Path(gtx.Ops), Width: float32(dp(gtx, 1))}.Op())
	}
}

func gradient(gtx layout.Context, bounds image.Rectangle, radius int, start, end, border color.NRGBA) {
	if bounds.Empty() {
		return
	}
	shape := clip.UniformRRect(bounds, dp(gtx, radius))
	stack := shape.Push(gtx.Ops)
	drawImage(gtx, gradientImage(start, end), bounds)
	stack.Pop()
	box(gtx, bounds, radius, color.NRGBA{}, border)
}

var gradients sync.Map

func gradientImage(start, end color.NRGBA) paint.ImageOp {
	key := [2]color.NRGBA{start, end}
	if cached, ok := gradients.Load(key); ok {
		return cached.(paint.ImageOp)
	}
	const size = 64
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	mix := func(a, b uint8, t float64) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
	for y := range size {
		for x := range size {
			t := float64(x+y) / (2 * (size - 1))
			img.SetNRGBA(x, y, color.NRGBA{
				R: mix(start.R, end.R, t), G: mix(start.G, end.G, t),
				B: mix(start.B, end.B, t), A: mix(start.A, end.A, t),
			})
		}
	}
	op := paint.NewImageOp(img)
	gradients.Store(key, op)
	return op
}

func drawImage(gtx layout.Context, img paint.ImageOp, dest image.Rectangle) {
	if img.Size().X == 0 || dest.Empty() {
		return
	}
	defer clip.Rect(dest).Push(gtx.Ops).Pop()
	defer op.Offset(dest.Min).Push(gtx.Ops).Pop()
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(
		float32(dest.Dx())/float32(img.Size().X), float32(dest.Dy())/float32(img.Size().Y),
	))).Push(gtx.Ops).Pop()
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

func backgroundImage(size image.Point) image.Image {
	img := image.NewRGBA(image.Rectangle{Max: size})
	aspect := float64(size.Y) / float64(size.X)
	upperRadius := math.Hypot(.85, 1.10*aspect) * .39
	lowerRadius := math.Hypot(1, aspect) * .36
	for y := range size.Y {
		for x := range size.X {
			u, v := float64(x)/float64(size.X), float64(y)/float64(size.Y)
			upperDistance := math.Hypot(u-.15, (v+.10)*aspect)
			lowerDistance := math.Hypot(1-u, (1-v)*aspect)
			a := .24 * max(0, 1-upperDistance/upperRadius)
			b := .10 * max(0, 1-lowerDistance/lowerRadius)
			c := color.RGBA{
				R: uint8(5 + (35-5)*a + (43-5)*b),
				G: uint8(10 + (110-10)*a + (188-10)*b),
				B: uint8(18 + (230-18)*a + (232-18)*b), A: 255,
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func orbGlow() image.Image {
	const size = 256
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			const radius = size / 2.0
			r := math.Hypot(float64(x)+.5-radius, float64(y)+.5-radius) / radius
			falloff := max(0, 1-r*r)
			opacity := .18 * falloff * falloff * falloff
			img.SetNRGBA(x, y, rgba(0x4ea4ff, uint8(math.Round(opacity*255))))
		}
	}
	return img
}

func radarSweep() image.Image {
	const size = 192
	const radius = size / 2.0
	const trail = math.Pi / 3
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			dx, dy := float64(x)+.5-radius, float64(y)+.5-radius
			angle := math.Atan2(dy, dx)
			if angle > 0 || angle < -trail {
				continue
			}
			r := math.Hypot(dx, dy) / radius
			edge := min(1, max(0, (1-r)*40))
			fade := 1 + angle/trail
			img.SetNRGBA(x, y, rgba(0x4e97ff, uint8(math.Round(66*fade*fade*edge))))
		}
	}
	return img
}

func circle(gtx layout.Context, center f32.Point, radius, width float32, tint color.NRGBA) {
	// Use fractional geometry so breathing rings and small status dots stay
	// round and move smoothly at any display scale.
	const k = .55228475
	r, offset := radius, radius*k
	x, y := center.X, center.Y
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(x+r, y))
	path.CubeTo(f32.Pt(x+r, y+offset), f32.Pt(x+offset, y+r), f32.Pt(x, y+r))
	path.CubeTo(f32.Pt(x-offset, y+r), f32.Pt(x-r, y+offset), f32.Pt(x-r, y))
	path.CubeTo(f32.Pt(x-r, y-offset), f32.Pt(x-offset, y-r), f32.Pt(x, y-r))
	path.CubeTo(f32.Pt(x+offset, y-r), f32.Pt(x+r, y-offset), f32.Pt(x+r, y))
	path.Close()
	spec := path.End()
	if width > 0 {
		paint.FillShape(gtx.Ops, tint, clip.Stroke{Path: spec, Width: width}.Op())
	} else {
		paint.FillShape(gtx.Ops, tint, clip.Outline{Path: spec}.Op())
	}
}
func (d *desktop) label(gtx layout.Context, bounds image.Rectangle, align layout.Direction, size int, weight font.Weight, fg color.NRGBA, value string) {
	if bounds.Empty() || value == "" {
		return
	}
	at(gtx, bounds.Min.X, bounds.Min.Y, func() {
		ctx := gtx
		ctx.Constraints = layout.Exact(bounds.Size())
		aligned := align
		if align == layout.W || align == layout.E {
			aligned = layout.Center
		}
		aligned.Layout(ctx, func(ctx layout.Context) layout.Dimensions {
			label := material.Label(d.theme, unit.Sp(size), value)
			label.Color, label.Font.Weight, label.MaxLines = fg, weight, 1
			if align == layout.W || align == layout.E {
				ctx.Constraints.Min.X = ctx.Constraints.Max.X
				if align == layout.E {
					label.Alignment = text.End
				}
			}
			return label.Layout(ctx)
		})
	})
}

func (d *desktop) width(gtx layout.Context, size int, weight font.Weight, value string) int {
	macro := op.Record(gtx.Ops)
	ctx := gtx
	ctx.Constraints.Min = image.Point{}
	label := material.Label(d.theme, unit.Sp(size), value)
	label.Font.Weight, label.MaxLines = weight, 1
	width := label.Layout(ctx).Size.X
	macro.Stop()
	return width
}

var glyphStrokes = map[string][][]f32.Point{
	"tv":         {{{X: 8, Y: 21}, {X: 16, Y: 21}}, {{X: 12, Y: 18}, {X: 12, Y: 21}}},
	"search":     {{{X: 16.4, Y: 16.4}, {X: 20, Y: 20}}},
	"chevron":    {{{X: 9, Y: 6}, {X: 15, Y: 12}, {X: 9, Y: 18}}},
	"freeze":     {{{X: 8, Y: 4}, {X: 8, Y: 20}}, {{X: 16, Y: 4}, {X: 16, Y: 20}}},
	"play":       {{{X: 8, Y: 5}, {X: 19, Y: 12}, {X: 8, Y: 19}, {X: 8, Y: 5}}},
	"disconnect": {{{X: 10, Y: 5}, {X: 5, Y: 5}, {X: 5, Y: 19}, {X: 10, Y: 19}}, {{X: 14, Y: 8}, {X: 18, Y: 12}, {X: 14, Y: 16}}, {{X: 8, Y: 12}, {X: 18, Y: 12}}},
	"mute":       {{{X: 4, Y: 9}, {X: 4, Y: 15}, {X: 8, Y: 15}, {X: 13, Y: 19}, {X: 13, Y: 5}, {X: 8, Y: 9}, {X: 4, Y: 9}}, {{X: 17, Y: 9}, {X: 22, Y: 15}}, {{X: 22, Y: 9}, {X: 17, Y: 15}}},
	"sound":      {{{X: 4, Y: 9}, {X: 4, Y: 15}, {X: 8, Y: 15}, {X: 13, Y: 19}, {X: 13, Y: 5}, {X: 8, Y: 9}, {X: 4, Y: 9}}, {{X: 17, Y: 9}, {X: 18.5, Y: 12}, {X: 17, Y: 15}}, {{X: 19.5, Y: 6}, {X: 21.5, Y: 9}, {X: 22, Y: 12}, {X: 21.5, Y: 15}, {X: 19.5, Y: 18}}},
	"lock":       {{{X: 8, Y: 10}, {X: 8, Y: 7}, {X: 9, Y: 4.5}, {X: 12, Y: 3}, {X: 15, Y: 4.5}, {X: 16, Y: 7}, {X: 16, Y: 10}}, {{X: 12, Y: 14}, {X: 12, Y: 17}}},
	"alert":      {{{X: 12, Y: 6.5}, {X: 12, Y: 13.5}}, {{X: 12, Y: 17}, {X: 12, Y: 17.6}}},
	"check":      {{{X: 5, Y: 12.5}, {X: 10, Y: 17.5}, {X: 19, Y: 7}}},
	"copy":       {{{X: 5, Y: 15}, {X: 4, Y: 15}, {X: 4, Y: 4}, {X: 15, Y: 4}, {X: 15, Y: 5}}},
}

func glyph(gtx layout.Context, bounds image.Rectangle, name string, fg color.NRGBA) {
	scale := float32(bounds.Dx()) / 24
	defer op.Offset(bounds.Min).Push(gtx.Ops).Pop()
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
	rounded := func(r image.Rectangle, radius int) {
		path := clip.UniformRRect(r, radius).Path(gtx.Ops)
		paint.FillShape(gtx.Ops, fg, clip.Stroke{Path: path, Width: 1.8}.Op())
	}
	switch name {
	case "tv":
		rounded(image.Rect(2, 4, 22, 18), 3)
	case "search":
		rounded(image.Rect(4, 4, 18, 18), 7)
	case "lock":
		rounded(image.Rect(4, 10, 20, 21), 3)
	case "copy":
		rounded(image.Rect(9, 9, 20, 20), 2)
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	for _, stroke := range glyphStrokes[name] {
		path.MoveTo(stroke[0])
		for _, point := range stroke[1:] {
			path.LineTo(point)
		}
	}
	paint.FillShape(gtx.Ops, fg, clip.Stroke{Path: path.End(), Width: 1.8}.Op())
}
