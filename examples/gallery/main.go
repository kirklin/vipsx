// Command gallery runs a broad set of libvips operations through vipsx and
// writes the results next to an index page, so the output can be looked at
// rather than read about.
//
// Each entry names the govips ImageRef method it corresponds to. That mapping
// is the point: everything those bindings expose as a dedicated method is one
// vips.Call here, with no per-operation code on this side.
//
//	go run ./examples/gallery <source-image> <output-dir>
package main

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kirklin/vipsx/vips"
)

// demo is one entry in the gallery.
type demo struct {
	Name   string // libvips operation, or a short label for a composed one
	Govips string // the govips ImageRef method this stands in for
	Code   string // what a caller writes
	Run    func(src *vips.Image) (*vips.Image, error)

	// filled in as we go
	File     string
	Width    int
	Height   int
	Bytes    int
	Duration string
	Err      string
}

// one is the common shape: feed the source in, take the image out.
//
// The argument names are not assumed. Most operations call their image input
// "in" but smartcrop and extract_area call it "input", and libvips is asked
// rather than guessed at.
func one(op string, args ...vips.Arg) func(*vips.Image) (*vips.Image, error) {
	return func(src *vips.Image) (*vips.Image, error) {
		spec, err := vips.Describe(op)
		if err != nil {
			return nil, err
		}
		var inName, outName string
		for _, a := range spec.Args {
			if a.Deprecated || a.Kind != vips.KindImage || !a.Required {
				continue
			}
			if a.Input && inName == "" {
				inName = a.Name
			}
			if a.Output && outName == "" {
				outName = a.Name
			}
		}
		if inName == "" || outName == "" {
			return nil, fmt.Errorf("%s is not a one-image-in one-image-out operation", op)
		}

		outs, err := vips.Call(op, append([]vips.Arg{vips.In(inName, src)}, args...)...)
		if err != nil {
			return nil, err
		}
		return outs.Image(outName)
	}
}

func demos() []demo {
	return []demo{
		{
			Name: "resize", Govips: "Resize",
			Code: `vips.Call("resize", vips.In("in", im), vips.In("scale", 0.5))`,
			Run:  one("resize", vips.In("scale", 0.5)),
		},
		{
			Name: "resize (nearest)", Govips: "Resize + KernelNearest",
			Code: `vips.In("kernel", 0)  // an explicit zero, and it means it`,
			Run:  one("resize", vips.In("scale", 0.25), vips.In("kernel", 0)),
		},
		{
			Name: "thumbnail_image", Govips: "Thumbnail",
			Code: `vips.Call("thumbnail_image", vips.In("in", im), vips.In("width", 400))`,
			Run:  one("thumbnail_image", vips.In("width", 400)),
		},
		{
			Name: "smartcrop", Govips: "SmartCrop",
			Code: `vips.Call("smartcrop", vips.In("in", im), vips.In("width", 400), vips.In("height", 400), vips.In("interesting", 3))`,
			Run:  one("smartcrop", vips.In("width", 400), vips.In("height", 400), vips.In("interesting", 3)),
		},
		{
			Name: "extract_area", Govips: "Crop / ExtractArea",
			Code: `vips.Call("extract_area", vips.In("in", im), vips.In("left", 80), vips.In("top", 80), vips.In("width", 320), vips.In("height", 240))`,
			Run:  one("extract_area", vips.In("left", 80), vips.In("top", 80), vips.In("width", 320), vips.In("height", 240)),
		},
		{
			Name: "rot", Govips: "Rotate",
			Code: `vips.Call("rot", vips.In("in", im), vips.In("angle", 1)) // 90 degrees`,
			Run:  one("rot", vips.In("angle", 1)),
		},
		{
			Name: "rotate", Govips: "Similarity",
			Code: `vips.Call("rotate", vips.In("in", im), vips.In("angle", 12.5))`,
			Run:  one("rotate", vips.In("angle", 12.5)),
		},
		{
			Name: "flip", Govips: "Flip",
			Code: `vips.Call("flip", vips.In("in", im), vips.In("direction", 1)) // vertical`,
			Run:  one("flip", vips.In("direction", 1)),
		},
		{
			Name: "autorot", Govips: "AutoRotate",
			Code: `vips.Call("autorot", vips.In("in", im))`,
			Run:  one("autorot"),
		},
		{
			Name: "embed", Govips: "Embed / EmbedBackground",
			Code: `vips.Call("embed", ..., vips.In("extend", 4)) // white border`,
			Run: one("embed", vips.In("x", 60), vips.In("y", 60),
				vips.In("width", 720), vips.In("height", 720), vips.In("extend", 4)),
		},
		{
			Name: "gravity", Govips: "Gravity",
			Code: `vips.Call("gravity", vips.In("in", im), vips.In("direction", 4), vips.In("width", 700), vips.In("height", 700))`,
			Run: one("gravity", vips.In("direction", 4),
				vips.In("width", 700), vips.In("height", 700)),
		},
		{
			Name: "replicate", Govips: "Replicate",
			Code: `vips.Call("replicate", vips.In("in", im), vips.In("across", 2), vips.In("down", 2))`,
			Run:  one("replicate", vips.In("across", 2), vips.In("down", 2)),
		},
		{
			Name: "zoom", Govips: "Zoom",
			Code: `vips.Call("zoom", vips.In("input", im), vips.In("xfac", 2), vips.In("yfac", 2))`,
			Run:  one("zoom", vips.In("xfac", 2), vips.In("yfac", 2)),
		},
		{
			Name: "gaussblur", Govips: "GaussianBlur",
			Code: `vips.Call("gaussblur", vips.In("in", im), vips.In("sigma", 4.0))`,
			Run:  one("gaussblur", vips.In("sigma", 4.0)),
		},
		{
			Name: "sharpen", Govips: "Sharpen",
			Code: `vips.Call("sharpen", vips.In("in", im), vips.In("sigma", 1.5), vips.In("m2", 8.0))`,
			Run:  one("sharpen", vips.In("sigma", 1.5), vips.In("m2", 8.0)),
		},
		{
			Name: "sobel", Govips: "Sobel",
			Code: `vips.Call("sobel", vips.In("in", im))`,
			Run:  one("sobel"),
		},
		{
			Name: "canny", Govips: "(not in govips)",
			Code: `vips.Call("canny", vips.In("in", im), vips.In("sigma", 1.4))`,
			Run:  one("canny", vips.In("sigma", 1.4)),
		},
		{
			Name: "invert", Govips: "Invert",
			Code: `vips.Call("invert", vips.In("in", im))`,
			Run:  one("invert"),
		},
		{
			Name: "gamma", Govips: "Gamma",
			Code: `vips.Call("gamma", vips.In("in", im), vips.In("exponent", 2.2))`,
			Run:  one("gamma", vips.In("exponent", 2.2)),
		},
		{
			Name: "linear", Govips: "Linear / Linear1",
			Code: `vips.Call("linear", vips.In("in", im), vips.In("a", []float64{1.4, 1, 0.8}), vips.In("b", []float64{-20, 0, 20}))`,
			Run: one("linear", vips.In("a", []float64{1.4, 1, 0.8}),
				vips.In("b", []float64{-20, 0, 20})),
		},
		{
			Name: "colourspace b-w", Govips: "ToColorSpace",
			Code: `vips.Call("colourspace", vips.In("in", im), vips.In("space", 1)) // b-w`,
			Run:  one("colourspace", vips.In("space", 1)),
		},
		{
			Name: "rank (median)", Govips: "Rank",
			Code: `vips.Call("rank", vips.In("in", im), vips.In("width", 5), vips.In("height", 5), vips.In("index", 12))`,
			Run:  one("rank", vips.In("width", 5), vips.In("height", 5), vips.In("index", 12)),
		},
		{
			Name: "recomb (sepia)", Govips: "Recomb",
			Code: `vips.Call("recomb", vips.In("in", im), vips.In("m", sepiaMatrix))`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				m, err := matrix3x3([]float64{
					0.393, 0.769, 0.189,
					0.349, 0.686, 0.168,
					0.272, 0.534, 0.131,
				})
				if err != nil {
					return nil, err
				}
				defer m.Close()
				outs, err := vips.Call("recomb", vips.In("in", src), vips.In("m", m))
				if err != nil {
					return nil, err
				}
				return outs.Image("out")
			},
		},
		{
			Name: "hist_equal", Govips: "HistogramNormalise",
			Code: `vips.Call("hist_equal", vips.In("in", im))`,
			Run:  one("hist_equal"),
		},
		{
			Name: "hist_find band 0", Govips: "HistogramFind",
			Code: `vips.In("band", 0)  // one band, not all of them`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				outs, err := vips.Call("hist_find", vips.In("in", src), vips.In("band", 0))
				if err != nil {
					return nil, err
				}
				h, err := outs.Image("out")
				if err != nil {
					return nil, err
				}
				defer h.Close()
				// stretch the histogram into something visible
				norm, err := vips.Call("hist_norm", vips.In("in", h))
				if err != nil {
					return nil, err
				}
				n, err := norm.Image("out")
				if err != nil {
					return nil, err
				}
				defer n.Close()
				plot, err := vips.Call("hist_plot", vips.In("in", n))
				if err != nil {
					return nil, err
				}
				return plot.Image("out")
			},
		},
		{
			Name: "flatten", Govips: "Flatten",
			Code: `vips.Call("flatten", vips.In("in", im), vips.In("background", []float64{255, 0, 0}))`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				withAlpha, err := vips.Call("addalpha", vips.In("in", src))
				if err != nil {
					return nil, err
				}
				a, err := withAlpha.Image("out")
				if err != nil {
					return nil, err
				}
				defer a.Close()
				outs, err := vips.Call("flatten", vips.In("in", a),
					vips.In("background", []float64{255, 0, 0}))
				if err != nil {
					return nil, err
				}
				return outs.Image("out")
			},
		},
		{
			Name: "extract_band", Govips: "ExtractBand",
			Code: `vips.Call("extract_band", vips.In("in", im), vips.In("band", 0))`,
			Run:  one("extract_band", vips.In("band", 0)),
		},
		{
			Name: "bandjoin_const", Govips: "BandJoinConst",
			Code: `vips.Call("bandjoin_const", vips.In("in", im), vips.In("c", []float64{128}))`,
			Run:  one("bandjoin_const", vips.In("c", []float64{128})),
		},
		{
			Name: "composite2", Govips: "Composite",
			Code: `vips.Call("composite2", vips.In("base", im), vips.In("overlay", blurred), vips.In("mode", 13))`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				blur, err := vips.Call("gaussblur", vips.In("in", src), vips.In("sigma", 12.0))
				if err != nil {
					return nil, err
				}
				b, err := blur.Image("out")
				if err != nil {
					return nil, err
				}
				defer b.Close()
				outs, err := vips.Call("composite2",
					vips.In("base", src), vips.In("overlay", b), vips.In("mode", 13))
				if err != nil {
					return nil, err
				}
				return outs.Image("out")
			},
		},
		{
			Name: "text + composite2", Govips: "Label",
			Code: `vips.Call("text", vips.In("text", "vipsx"), ...) then composite2`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				txt, err := vips.Call("text",
					vips.In("text", "vipsx"),
					vips.In("font", "sans bold 72"),
					vips.In("rgba", true),
				)
				if err != nil {
					return nil, err
				}
				t, err := txt.Image("out")
				if err != nil {
					return nil, err
				}
				defer t.Close()

				base, err := vips.Call("addalpha", vips.In("in", src))
				if err != nil {
					return nil, err
				}
				b, err := base.Image("out")
				if err != nil {
					return nil, err
				}
				defer b.Close()

				outs, err := vips.Call("composite2",
					vips.In("base", b), vips.In("overlay", t),
					vips.In("mode", 2), vips.In("x", 40), vips.In("y", 40))
				if err != nil {
					return nil, err
				}
				return outs.Image("out")
			},
		},
		{
			Name: "draw_rect", Govips: "DrawRect",
			Code: `vips.Call("draw_rect", vips.In("image", copy), vips.In("ink", []float64{255, 0, 0}), ...)`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				// draw_* mutate their argument, so work on a private copy
				cp, err := vips.Call("copy", vips.In("in", src))
				if err != nil {
					return nil, err
				}
				c, err := cp.Image("out")
				if err != nil {
					return nil, err
				}
				outs, err := vips.Call("draw_rect",
					vips.In("image", c),
					vips.In("ink", []float64{255, 0, 0}),
					vips.In("left", 60), vips.In("top", 60),
					vips.In("width", 300), vips.In("height", 200),
					vips.In("fill", false),
				)
				if err != nil {
					c.Close()
					return nil, err
				}
				outs.Close()
				return c, nil
			},
		},
		{
			Name: "arrayjoin", Govips: "ArrayJoin",
			Code: `vips.Call("arrayjoin", vips.In("in", []*vips.Image{a, b}), vips.In("across", 2))`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				gray, err := vips.Call("colourspace", vips.In("in", src), vips.In("space", 1))
				if err != nil {
					return nil, err
				}
				g, err := gray.Image("out")
				if err != nil {
					return nil, err
				}
				defer g.Close()
				rgb, err := vips.Call("colourspace", vips.In("in", g), vips.In("space", 22))
				if err != nil {
					return nil, err
				}
				r, err := rgb.Image("out")
				if err != nil {
					return nil, err
				}
				defer r.Close()
				outs, err := vips.Call("arrayjoin",
					vips.In("in", []*vips.Image{src, r}), vips.In("across", 2))
				if err != nil {
					return nil, err
				}
				return outs.Image("out")
			},
		},
		{
			Name: "similarity", Govips: "Similarity",
			Code: `vips.Call("similarity", vips.In("in", im), vips.In("scale", 0.6), vips.In("angle", 20.0))`,
			Run:  one("similarity", vips.In("scale", 0.6), vips.In("angle", 20.0)),
		},
		{
			Name: "affine", Govips: "(not in govips)",
			Code: `vips.Call("affine", vips.In("in", im), vips.In("matrix", []float64{1, 0.3, 0, 1}))`,
			Run:  one("affine", vips.In("matrix", []float64{1, 0.3, 0, 1})),
		},
		{
			Name: "falsecolour", Govips: "(not in govips)",
			Code: `vips.Call("falsecolour", vips.In("in", gray))`,
			Run: func(src *vips.Image) (*vips.Image, error) {
				gray, err := vips.Call("colourspace", vips.In("in", src), vips.In("space", 1))
				if err != nil {
					return nil, err
				}
				g, err := gray.Image("out")
				if err != nil {
					return nil, err
				}
				defer g.Close()
				outs, err := vips.Call("falsecolour", vips.In("in", g))
				if err != nil {
					return nil, err
				}
				return outs.Image("out")
			},
		},
	}
}

// matrix3x3 builds a small matrix image, which is how libvips takes convolution
// and recombination kernels.
func matrix3x3(values []float64) (*vips.Image, error) {
	outs, err := vips.Call("black", vips.In("width", 3), vips.In("height", 3))
	if err != nil {
		return nil, err
	}
	defer outs.Close()
	black, err := outs.Image("out")
	if err != nil {
		return nil, err
	}
	defer black.Close()

	cast, err := vips.Call("cast", vips.In("in", black), vips.In("format", 8)) // double, not dpcomplex
	if err != nil {
		return nil, err
	}
	// Deliberately not closing cast: its image is what this returns, and the
	// caller owns it from here.
	m, err := cast.Image("out")
	if err != nil {
		cast.Close()
		return nil, err
	}

	for i, v := range values {
		res, err := vips.Call("draw_rect",
			vips.In("image", m), vips.In("ink", []float64{v}),
			vips.In("left", i%3), vips.In("top", i/3),
			vips.In("width", 1), vips.In("height", 1), vips.In("fill", true))
		if err != nil {
			m.Close()
			return nil, err
		}
		res.Close()
	}
	return m, nil
}

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s <source-image> <output-dir>", os.Args[0])
	}
	srcPath, outDir := os.Args[1], os.Args[2]

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	full, err := vips.LoadFile(srcPath)
	if err != nil {
		log.Fatal(err)
	}
	defer full.Close()

	// Work at a manageable size so the page stays light.
	scaled, err := vips.Call("thumbnail_image", vips.In("in", full), vips.In("width", 600))
	if err != nil {
		log.Fatal(err)
	}
	defer scaled.Close()
	src, err := scaled.Image("out")
	if err != nil {
		log.Fatal(err)
	}

	if err := vips.SaveFile(src, filepath.Join(outDir, "00-source.jpg"),
		vips.In("Q", 88)); err != nil {
		log.Fatal(err)
	}

	list := demos()
	ok, failed := 0, 0
	for i := range list {
		d := &list[i]
		name := fmt.Sprintf("%02d-%s", i+1, strings.NewReplacer(
			" ", "-", "(", "", ")", "", "+", "and").Replace(d.Name))
		d.File = name + ".jpg"

		start := time.Now()
		res, err := d.Run(src)
		if err != nil {
			d.Err = err.Error()
			d.File = ""
			failed++
			fmt.Printf("  %-24s FAILED  %v\n", d.Name, err)
			continue
		}
		d.Duration = time.Since(start).Round(time.Microsecond).String()

		// Some operations return formats a JPEG cannot hold; normalise first.
		flat, err := forJPEG(res)
		if err != nil {
			res.Close()
			d.Err = err.Error()
			d.File = ""
			failed++
			continue
		}

		if err := vips.SaveFile(flat, filepath.Join(outDir, d.File), vips.In("Q", 88)); err != nil {
			d.Err = err.Error()
			d.File = ""
			failed++
			flat.Close()
			res.Close()
			continue
		}
		if fi, err := os.Stat(filepath.Join(outDir, d.File)); err == nil {
			d.Bytes = int(fi.Size())
		}
		d.Width, d.Height = flat.Width(), flat.Height()
		ok++
		fmt.Printf("  %-24s %4dx%-4d  %6s  %s\n", d.Name, d.Width, d.Height, d.Duration, d.File)

		flat.Close()
		res.Close()
	}

	sort.SliceStable(list, func(i, j int) bool { return list[i].Err == "" && list[j].Err != "" })

	if err := writeIndex(outDir, srcPath, list, ok, failed); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d succeeded, %d failed. Open %s\n",
		ok, failed, filepath.Join(outDir, "index.html"))
}

// forJPEG coerces whatever an operation returned into something JPEG accepts:
// eight bits per sample, one or three bands, no alpha.
func forJPEG(im *vips.Image) (*vips.Image, error) {
	cur := im
	owned := false
	step := func(op string, args ...vips.Arg) error {
		all := append([]vips.Arg{vips.In("in", cur)}, args...)
		outs, err := vips.Call(op, all...)
		if err != nil {
			return err
		}
		next, err := outs.Image("out")
		if err != nil {
			return err
		}
		if owned {
			cur.Close()
		}
		cur, owned = next, true
		return nil
	}

	if cur.Bands() == 2 || cur.Bands() == 4 {
		if err := step("flatten", vips.In("background", []float64{255, 255, 255})); err != nil {
			return nil, err
		}
	}
	if cur.Bands() > 3 {
		if err := step("extract_band", vips.In("band", 0), vips.In("n", 3)); err != nil {
			return nil, err
		}
	}
	// scale maps any numeric range into 0-255, which is what makes float and
	// complex results such as canny or the histograms viewable at all.
	if err := step("scale"); err != nil {
		// scale rejects some inputs; a plain cast is the fallback
		if err := step("cast", vips.In("format", 0)); err != nil {
			return nil, err
		}
	}
	if !owned {
		outs, err := vips.Call("copy", vips.In("in", cur))
		if err != nil {
			return nil, err
		}
		return outs.Image("out")
	}
	return cur, nil
}

const indexHTML = `<!doctype html>
<meta charset="utf-8">
<title>vipsx gallery</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
         margin: 0 auto; padding: 2.5rem 1.5rem; max-width: 1200px; }
  h1 { font-size: 1.6rem; margin: 0 0 .3rem; }
  .sub { opacity: .65; margin: 0 0 2rem; }
  .grid { display: grid; gap: 1.5rem;
          grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); }
  figure { margin: 0; border: 1px solid color-mix(in srgb, currentColor 15%, transparent);
           border-radius: 10px; overflow: hidden; }
  figure img { display: block; width: 100%; height: auto; background: #8883; }
  figcaption { padding: .7rem .85rem .85rem; }
  .op { font-weight: 600; }
  .meta { opacity: .6; font-size: .82rem; margin-top: .15rem; }
  .govips { font-size: .82rem; opacity: .75; margin-top: .3rem; }
  code { font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace;
         display: block; margin-top: .5rem; padding: .5rem .6rem; border-radius: 6px;
         background: color-mix(in srgb, currentColor 7%, transparent);
         overflow-x: auto; white-space: pre; }
  .failed { opacity: .55; }
  .err { color: #c0392b; font-size: .82rem; margin-top: .3rem; }
</style>
<h1>vipsx gallery</h1>
<p class="sub">
  {{.OK}} operations succeeded, {{.Failed}} failed &middot;
  libvips {{.Version}} &middot; {{.Ops}} operations reachable &middot;
  source <code style="display:inline;padding:.1rem .3rem">{{.Source}}</code>
</p>

<div class="grid">
  <figure>
    <img src="00-source.jpg" alt="source">
    <figcaption><span class="op">source</span>
      <div class="meta">the input every entry below starts from</div>
    </figcaption>
  </figure>
{{range .Demos}}
  <figure{{if .Err}} class="failed"{{end}}>
    {{if .File}}<img src="{{.File}}" alt="{{.Name}}" loading="lazy">{{end}}
    <figcaption>
      <span class="op">{{.Name}}</span>
      {{if .Err}}<div class="err">{{.Err}}</div>
      {{else}}<div class="meta">{{.Width}}&times;{{.Height}} &middot; {{.Bytes}} bytes &middot; {{.Duration}}</div>{{end}}
      <div class="govips">govips: <b>{{.Govips}}</b></div>
      <code>{{.Code}}</code>
    </figcaption>
  </figure>
{{end}}
</div>
`

func writeIndex(dir, source string, list []demo, ok, failed int) error {
	t, err := template.New("index").Parse(indexHTML)
	if err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()

	return t.Execute(f, struct {
		Demos          []demo
		OK, Failed     int
		Version, Ops   string
		Source         string
		OpsCountHolder int
	}{
		Demos:   list,
		OK:      ok,
		Failed:  failed,
		Version: vips.Version(),
		Ops:     fmt.Sprint(len(vips.Operations())),
		Source:  filepath.Base(source),
	})
}
