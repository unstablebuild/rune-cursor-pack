package shaders_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

// ReadPixels needs a running graphics backend, not a no-op draw driver.
type testGame struct {
	run  func() int
	code int
}

func (g *testGame) Update() error {
	g.code = g.run()
	return ebiten.Termination
}

func (*testGame) Draw(*ebiten.Image) {}

func (*testGame) Layout(int, int) (int, int) { return 64, 64 }

func TestMain(m *testing.M) {
	ebiten.SetWindowTitle("Rune cursor shader tests")
	ebiten.SetWindowSize(64, 64)
	g := &testGame{run: m.Run, code: 1}
	if err := ebiten.RunGame(g); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(g.code)
}

func loadShader(t *testing.T, name string) *ebiten.Shader {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "shaders", name+".kage"))
	if err != nil {
		t.Fatal(err)
	}
	shader, err := ebiten.NewShader(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(shader.Dispose)
	return shader
}

func TestPackCompiles(t *testing.T) {
	paths, err := filepath.Glob("../shaders/*.kage")
	if err != nil || len(paths) == 0 {
		t.Fatalf("shader files: %v, %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			name := filepath.Base(path)
			loadShader(t, name[:len(name)-len(".kage")])
		})
	}
}

func TestPortUniformsAndThresholds(t *testing.T) {
	want := map[string]string{
		"Time": "float", "Duration": "float", "Cursor": "vec4",
		"PreviousCursor": "vec4", "CursorColor": "vec4",
	}
	for _, name := range []string{"smear"} {
		t.Run(name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "../shaders/"+name+".kage", nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			thresholds := map[string]string{}
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, id := range value.Names {
						if gen.Tok == token.VAR {
							typ, ok := value.Type.(*ast.Ident)
							if !ok || want[id.Name] == "" || typ.Name != want[id.Name] {
								t.Errorf("unsupported Rune uniform %s: %v", id.Name, value.Type)
							}
						}
						if gen.Tok == token.CONST && (id.Name == "MinJumpRows" || id.Name == "MinJumpColumns") {
							lit, ok := value.Values[i].(*ast.BasicLit)
							if !ok || lit.Kind != token.INT {
								t.Fatalf("%s must be an integer literal", id.Name)
							}
							thresholds[id.Name] = lit.Value
						}
					}
				}
			}
			if thresholds["MinJumpRows"] != "2" || thresholds["MinJumpColumns"] != "9" {
				t.Fatalf("unexpected jump thresholds: %v", thresholds)
			}
		})
	}
}

const width, height = 320, 240

type frame struct {
	time, duration          float32
	cursor, previous, color [4]float32
}

func defaultFrame() frame {
	return frame{
		time: 0.06, duration: 0.3,
		cursor: [4]float32{220, 140, 10, 20}, previous: [4]float32{60, 60, 10, 20},
		color: [4]float32{0.4, 0.7, 0.9, 1},
	}
}

func background(alpha byte) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = byte((24 + x%16) * int(alpha) / 255)
			img.Pix[i+1] = byte((24 + y%16) * int(alpha) / 255)
			img.Pix[i+2] = byte(48 * int(alpha) / 255)
			img.Pix[i+3] = alpha
		}
	}
	return img
}

func render(t *testing.T, shader *ebiten.Shader, f frame, bg *image.RGBA) *image.RGBA {
	t.Helper()
	// Use a subimage to catch accidental use of source texture coordinates
	// for cursor geometry. Ebitengine may also place images in a texture atlas.
	backing := ebiten.NewImage(width+12, height+16)
	defer backing.Dispose()
	source := backing.SubImage(image.Rect(5, 7, width+5, height+7)).(*ebiten.Image)
	source.WritePixels(bg.Pix)
	dst := ebiten.NewImage(width, height)
	defer dst.Dispose()
	opts := &ebiten.DrawRectShaderOptions{
		Blend: ebiten.BlendCopy,
		Uniforms: map[string]any{
			"Time": f.time, "Duration": f.duration,
			"Cursor": f.cursor[:], "PreviousCursor": f.previous[:], "CursorColor": f.color[:],
		},
	}
	opts.Images[0] = source
	dst.DrawRectShader(width, height, shader, opts)
	result := image.NewRGBA(bg.Bounds())
	dst.ReadPixels(result.Pix)
	return result
}

func TestInactiveFramesPreserveWindow(t *testing.T) {
	shader := loadShader(t, "smear")
	cases := map[string]func(*frame){
		"end":                func(f *frame) { f.time = f.duration },
		"after_end":          func(f *frame) { f.time = f.duration * 2 },
		"zero_duration":      func(f *frame) { f.duration = 0 },
		"negative_duration":  func(f *frame) { f.duration = -1 },
		"zero_width":         func(f *frame) { f.cursor[2] = 0 },
		"zero_height":        func(f *frame) { f.cursor[3] = 0 },
		"same_position":      func(f *frame) { f.previous = f.cursor },
		"transparent_cursor": func(f *frame) { f.color = [4]float32{} },
	}
	for caseName, change := range cases {
		t.Run(caseName, func(t *testing.T) {
			for _, alpha := range []byte{0, 128, 255} {
				f := defaultFrame()
				change(&f)
				bg := background(alpha)
				if got := render(t, shader, f, bg); !bytes.Equal(got.Pix, bg.Pix) {
					t.Fatalf("inactive shader changed window (alpha %d)", alpha)
				}
			}
		})
	}
}

func TestSmearDirectionsAndShapes(t *testing.T) {
	shader := loadShader(t, "smear")
	bg := background(255)
	shapes := [][2]float32{{10, 20}, {2, 20}, {10, 2}}
	directions := [][2]float32{{100, 0}, {-100, 0}, {0, 80}, {0, -80}, {100, 80}, {-100, 80}, {100, -80}, {-100, -80}}
	for _, shape := range shapes {
		for _, dir := range directions {
			for _, progress := range []float32{0.05, 0.5, 0.6} {
				t.Run(fmt.Sprintf("%vx%v/%v,%v/%.2f", shape[0], shape[1], dir[0], dir[1], progress), func(t *testing.T) {
					f := defaultFrame()
					f.previous = [4]float32{150, 110, shape[0], shape[1]}
					f.cursor = [4]float32{150 + dir[0], 110 + dir[1], shape[0], shape[1]}
					f.time = f.duration * progress
					got := render(t, shader, f, bg)
					changed := 0
					for y := 0; y < height; y++ {
						for x := 0; x < width; x++ {
							if got.RGBAAt(x, y) == bg.RGBAAt(x, y) {
								continue
							}
							changed++
							px, py := float32(x)+0.5, float32(y)+0.5
							if px >= f.cursor[0] && px <= f.cursor[0]+shape[0] && py >= f.cursor[1] && py <= f.cursor[1]+shape[1] {
								t.Fatalf("cursor interior changed at %d,%d", x, y)
							}
							if px < min(f.previous[0], f.cursor[0])-1 || px > max(f.previous[0], f.cursor[0])+shape[0]+1 || py < min(f.previous[1], f.cursor[1])-1 || py > max(f.previous[1], f.cursor[1])+shape[1]+1 {
								t.Fatalf("trail escaped bounds at %d,%d", x, y)
							}
						}
					}
					if changed == 0 {
						t.Fatal("smear disappeared before animation completed")
					}
					if progress == 0.05 {
						x := int((f.previous[0] + f.cursor[0] + shape[0]) / 2)
						y := int((f.previous[1] + f.cursor[1] + shape[1]) / 2)
						if got.RGBAAt(x, y) == bg.RGBAAt(x, y) {
							t.Fatal("missing solid smear at jump midpoint")
						}
					}
				})
			}
		}
	}
}

func TestSmearChangingShape(t *testing.T) {
	shader := loadShader(t, "smear")
	bg := background(255)
	cases := []struct {
		name             string
		previous, cursor [4]float32
		x, y             int
	}{
		{"underline_to_block", [4]float32{60, 60, 10, 2}, [4]float32{160, 60, 10, 20}, 115, 65},
		{"block_to_underline", [4]float32{60, 60, 10, 20}, [4]float32{160, 60, 10, 2}, 115, 65},
		{"bar_to_block", [4]float32{60, 60, 2, 20}, [4]float32{60, 160, 10, 20}, 64, 115},
		{"block_to_bar", [4]float32{60, 60, 10, 20}, [4]float32{60, 160, 2, 20}, 64, 115},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, progress := range []float32{0, 0.05} {
				f := defaultFrame()
				f.previous, f.cursor = tc.previous, tc.cursor
				f.time = f.duration * progress
				got := render(t, shader, f, bg)
				// This pixel is inside the six-sided trail, but outside the
				// intersection of its half-planes when the shape is concave.
				if got.RGBAAt(tc.x, tc.y) == bg.RGBAAt(tc.x, tc.y) {
					t.Fatalf("missing concave trail interior at progress %v", progress)
				}
			}
		})
	}
}

func TestSmearPremultipliedOverlay(t *testing.T) {
	shader := loadShader(t, "smear")
	for _, alpha := range []byte{0, 128, 255} {
		bg := background(alpha)
		f := defaultFrame()
		f.time = 0
		// Neutral gray makes the saturation boost a no-op. This is
		// premultiplied RGBA: straight RGB 0.5 at opacity 0.5.
		f.color = [4]float32{0.25, 0.25, 0.25, 0.5}
		got := render(t, shader, f, bg)
		x, y := 145, 110 // Solid interior at the jump midpoint.
		p, b := got.RGBAAt(x, y), bg.RGBAAt(x, y)
		want := []float32{63.75 + float32(b.R)*0.5, 63.75 + float32(b.G)*0.5, 63.75 + float32(b.B)*0.5, 127.5 + float32(b.A)*0.5}
		for i, value := range []byte{p.R, p.G, p.B, p.A} {
			diff := float32(value) - want[i]
			if diff < -1 || diff > 1 {
				t.Fatalf("alpha %d: incorrect source-over result %v, expected approximately %v", alpha, p, want)
			}
		}
		for i := 0; i < len(got.Pix); i += 4 {
			if got.Pix[i] > got.Pix[i+3] || got.Pix[i+1] > got.Pix[i+3] || got.Pix[i+2] > got.Pix[i+3] {
				t.Fatalf("non-premultiplied smear pixel at byte %d", i)
			}
		}
	}
}

// Optional real GPU snapshots for visual review; not README recordings.
func TestRenderSnapshots(t *testing.T) {
	dir := os.Getenv("RUNE_SHADER_TEST_IMAGES")
	if dir == "" {
		t.Skip("set RUNE_SHADER_TEST_IMAGES to save GPU-rendered PNG frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshots := []struct {
		name     string
		duration float32
		color    [4]float32
	}{
		{"smear", 0.3, defaultFrame().color},
		{"lightning_color", 0.35, [4]float32{0.63, 0.13, 0.94, 1}},
		{"lightning_color-red", 0.35, [4]float32{0.9, 0.2, 0.15, 1}},
	}
	for _, s := range snapshots {
		shader := loadShader(t, strings.TrimSuffix(s.name, "-red"))
		for _, progress := range []float32{0, 0.1, 0.13, 0.3, 0.5, 0.7, 0.95, 1} {
			f := defaultFrame()
			f.duration, f.color = s.duration, s.color
			f.time = f.duration * progress
			img := render(t, shader, f, background(255))
			path := filepath.Join(dir, fmt.Sprintf("%s-%03d.png", s.name, int(progress*100)))
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, img)
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("save snapshot: %v, %v", err, closeErr)
			}
		}
	}
}

func TestLightningColorFollowsCursor(t *testing.T) {
	shader := loadShader(t, "lightning_color")
	bg := background(255)
	// Sum the light the bolt adds, per channel, at the first return stroke.
	added := func(color [4]float32) [3]float64 {
		f := defaultFrame()
		f.duration, f.time, f.color = 0.35, 0.35*0.15, color
		got := render(t, shader, f, bg)
		var sum [3]float64
		for i := 0; i < len(got.Pix); i += 4 {
			for c := 0; c < 3; c++ {
				sum[c] += float64(got.Pix[i+c]) - float64(bg.Pix[i+c])
			}
		}
		return sum
	}
	for _, tc := range []struct {
		name     string
		color    [4]float32
		dominant int
	}{
		{"red", [4]float32{0.9, 0.2, 0.15, 1}, 0},
		{"green", [4]float32{0.1, 0.8, 0.2, 1}, 1},
		// Premultiplied: straight (0.2, 0.3, 1) at half opacity.
		{"translucent_blue", [4]float32{0.1, 0.15, 0.5, 0.5}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := added(tc.color)
			for c := 0; c < 3; c++ {
				if c != tc.dominant && sum[c] >= sum[tc.dominant] {
					t.Fatalf("bolt light %v is not dominated by channel %d", sum, tc.dominant)
				}
			}
		})
	}
	// Gray, black and transparent cursors all strike the white bolt.
	white := added([4]float32{1, 1, 1, 1})
	if white[0] <= 0 {
		t.Fatal("white cursor drew no bolt")
	}
	for _, color := range [][4]float32{{}, {0, 0, 0, 1}, {0.5, 0.5, 0.5, 1}} {
		if sum := added(color); sum != white {
			t.Fatalf("cursor %v: added light %v, want the white bolt's %v", color, sum, white)
		}
	}
}
