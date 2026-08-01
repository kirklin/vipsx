// Command gallery runs a broad set of libvips operations through vipsx and
// writes the results next to an index page, so the output can be looked at
// rather than read about.
//
// Everything here goes through the generated typed layer, which is how the
// library is meant to be used. Each entry names the govips ImageRef method it
// stands in for, so the mapping between the two can be read off the page.
//
//	go run ./examples/gallery <source-image> <output-dir>
package main

import (
	"flag"
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
	Name   string // what the entry shows
	Govips string // the govips ImageRef method this stands in for
	Code   string // the call, as a caller would write it
	Run    func(src *vips.Image) (*vips.Image, error)

	// filled in as we go
	File     string
	Width    int
	Height   int
	Bytes    int
	Duration string
	Err      string
}

func demos() []demo {
	return []demo{
		{
			Name: "Resize", Govips: "Resize",
			Code: `vips.Resize(im, 0.5, nil)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Resize(im, 0.5, nil) },
		},
		{
			Name: "Resize, nearest neighbour", Govips: "Resize + KernelNearest",
			Code: `vips.Resize(im, 0.25, &vips.ResizeOptions{
    Kernel: vips.Ptr(vips.KernelNearest),  // KernelNearest is 0, and it is sent
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Resize(im, 0.25, &vips.ResizeOptions{
					Kernel: vips.Ptr(vips.KernelNearest),
				})
			},
		},
		{
			Name: "ThumbnailImage", Govips: "Thumbnail",
			Code: `vips.ThumbnailImage(im, 400, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.ThumbnailImage(im, 400, nil)
			},
		},
		{
			Name: "Smartcrop", Govips: "SmartCrop",
			Code: `vips.Smartcrop(im, 400, 400, &vips.SmartcropOptions{
    Interesting: vips.Ptr(vips.InterestingAttention),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Smartcrop(im, 400, 400, &vips.SmartcropOptions{
					Interesting: vips.Ptr(vips.InterestingAttention),
				})
			},
		},
		{
			Name: "ExtractArea", Govips: "Crop / ExtractArea",
			Code: `vips.ExtractArea(im, 80, 80, 320, 240)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.ExtractArea(im, 80, 80, 320, 240)
			},
		},
		{
			Name: "Rot", Govips: "Rotate",
			Code: `vips.Rot(im, vips.AngleD90)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Rot(im, vips.AngleD90) },
		},
		{
			Name: "Rotate, arbitrary angle", Govips: "Similarity",
			Code: `vips.Rotate(im, 12.5, nil)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Rotate(im, 12.5, nil) },
		},
		{
			Name: "Flip", Govips: "Flip",
			Code: `vips.Flip(im, vips.DirectionVertical)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Flip(im, vips.DirectionVertical)
			},
		},
		{
			Name: "Autorot", Govips: "AutoRotate",
			Code: `vips.Autorot(im, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Autorot(im, nil)
			},
		},
		{
			Name: "Embed", Govips: "Embed / EmbedBackground",
			Code: `vips.Embed(im, 60, 60, 720, 720, &vips.EmbedOptions{
    Extend: vips.Ptr(vips.ExtendWhite),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Embed(im, 60, 60, 720, 720, &vips.EmbedOptions{
					Extend: vips.Ptr(vips.ExtendWhite),
				})
			},
		},
		{
			Name: "Gravity", Govips: "Gravity",
			Code: `vips.Gravity(im, vips.CompassDirectionWest, 700, 700, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Gravity(im, vips.CompassDirectionWest, 700, 700, nil)
			},
		},
		{
			Name: "Replicate", Govips: "Replicate",
			Code: `vips.Replicate(im, 2, 2)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Replicate(im, 2, 2) },
		},
		{
			Name: "Zoom", Govips: "Zoom",
			Code: `vips.Zoom(im, 2, 2)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Zoom(im, 2, 2) },
		},
		{
			Name: "Gaussblur", Govips: "GaussianBlur",
			Code: `vips.Gaussblur(im, 4.0, nil)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Gaussblur(im, 4.0, nil) },
		},
		{
			Name: "Sharpen", Govips: "Sharpen",
			Code: `vips.Sharpen(im, &vips.SharpenOptions{
    Sigma: vips.Ptr(1.5), M2: vips.Ptr(8.0),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Sharpen(im, &vips.SharpenOptions{
					Sigma: vips.Ptr(1.5), M2: vips.Ptr(8.0),
				})
			},
		},
		{
			Name: "Sobel", Govips: "Sobel",
			Code: `vips.Sobel(im)`,
			Run:  vips.Sobel,
		},
		{
			Name: "Canny", Govips: "(not in govips)",
			Code: `vips.Canny(im, &vips.CannyOptions{Sigma: vips.Ptr(1.4)})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Canny(im, &vips.CannyOptions{Sigma: vips.Ptr(1.4)})
			},
		},
		{
			Name: "Invert", Govips: "Invert",
			Code: `vips.Invert(im)`,
			Run:  vips.Invert,
		},
		{
			Name: "Gamma", Govips: "Gamma",
			Code: `vips.Gamma(im, &vips.GammaOptions{Exponent: vips.Ptr(2.2)})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Gamma(im, &vips.GammaOptions{Exponent: vips.Ptr(2.2)})
			},
		},
		{
			Name: "Linear", Govips: "Linear / Linear1",
			Code: `vips.Linear(im, []float64{1.4, 1, 0.8}, []float64{-20, 0, 20}, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Linear(im, []float64{1.4, 1, 0.8}, []float64{-20, 0, 20}, nil)
			},
		},
		{
			Name: "Colourspace to b-w", Govips: "ToColorSpace",
			Code: `vips.Colourspace(im, vips.InterpretationBW, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Colourspace(im, vips.InterpretationBW, nil)
			},
		},
		{
			Name: "Rank, median filter", Govips: "Rank",
			Code: `vips.Rank(im, 5, 5, 12)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.Rank(im, 5, 5, 12) },
		},
		{
			Name: "Recomb, sepia", Govips: "Recomb",
			Code: `m, _ := matrix3x3(sepia...)
vips.Recomb(im, m)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				m, err := matrix3x3([]float64{
					0.393, 0.769, 0.189,
					0.349, 0.686, 0.168,
					0.272, 0.534, 0.131,
				})
				if err != nil {
					return nil, err
				}
				defer m.Close()
				return vips.Recomb(im, m)
			},
		},
		{
			Name: "HistEqual", Govips: "HistogramNormalise",
			Code: `vips.HistEqual(im, nil)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.HistEqual(im, nil) },
		},
		{
			Name: "HistFind on band 0", Govips: "HistogramFind",
			Code: `hist, _ := vips.HistFind(im, &vips.HistFindOptions{
    Band: vips.Ptr(0),  // band 0, not every band
})
norm, _ := vips.HistNorm(hist)
vips.HistPlot(norm)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				hist, err := vips.HistFind(im, &vips.HistFindOptions{Band: vips.Ptr(0)})
				if err != nil {
					return nil, err
				}
				defer hist.Close()
				norm, err := vips.HistNorm(hist)
				if err != nil {
					return nil, err
				}
				defer norm.Close()
				return vips.HistPlot(norm)
			},
		},
		{
			Name: "Flatten onto red", Govips: "Flatten",
			Code: `// bandjoin_const rather than addalpha: addalpha only became an
// operation after 8.15, and this runs on 8.12 too.
rgba, _ := vips.BandjoinConst(im, []float64{255})
vips.Flatten(rgba, &vips.FlattenOptions{
    Background: vips.Ptr([]float64{255, 0, 0}),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				rgba, err := vips.BandjoinConst(im, []float64{255})
				if err != nil {
					return nil, err
				}
				defer rgba.Close()
				return vips.Flatten(rgba, &vips.FlattenOptions{
					Background: vips.Ptr([]float64{255, 0, 0}),
				})
			},
		},
		{
			Name: "ExtractBand", Govips: "ExtractBand",
			Code: `vips.ExtractBand(im, 0, nil)`,
			Run:  func(im *vips.Image) (*vips.Image, error) { return vips.ExtractBand(im, 0, nil) },
		},
		{
			Name: "BandjoinConst", Govips: "BandJoinConst",
			Code: `vips.BandjoinConst(im, []float64{128})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.BandjoinConst(im, []float64{128})
			},
		},
		{
			Name: "Composite2, saturate", Govips: "Composite",
			Code: `blur, _ := vips.Gaussblur(im, 12.0, nil)
vips.Composite2(im, blur, vips.BlendModeSaturate, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				blur, err := vips.Gaussblur(im, 12.0, nil)
				if err != nil {
					return nil, err
				}
				defer blur.Close()
				return vips.Composite2(im, blur, vips.BlendModeSaturate, nil)
			},
		},
		{
			Name: "Text over an image", Govips: "Label",
			Code: `txt, _ := vips.Text("vipsx", &vips.TextOptions{
    Font: vips.Ptr("sans bold 72"), Rgba: vips.Ptr(true),
})
base, _ := vips.BandjoinConst(im, []float64{255})
vips.Composite2(base, txt, vips.BlendModeOver, &vips.Composite2Options{
    X: vips.Ptr(40), Y: vips.Ptr(40),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				txt, err := vips.Text("vipsx", &vips.TextOptions{
					Font: vips.Ptr("sans bold 72"),
					Rgba: vips.Ptr(true),
				})
				if err != nil {
					return nil, err
				}
				defer txt.Close()

				base, err := vips.BandjoinConst(im, []float64{255})
				if err != nil {
					return nil, err
				}
				defer base.Close()

				return vips.Composite2(base, txt, vips.BlendModeOver, &vips.Composite2Options{
					X: vips.Ptr(40),
					Y: vips.Ptr(40),
				})
			},
		},
		{
			Name: "DrawRect", Govips: "DrawRect",
			Code: `vips.DrawRect(im, []float64{255, 0, 0}, 60, 60, 300, 200, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				// Draws on a private copy and returns it; im is untouched.
				return vips.DrawRect(im, []float64{255, 0, 0}, 60, 60, 300, 200, nil)
			},
		},
		{
			Name: "Arrayjoin", Govips: "ArrayJoin",
			Code: `gray, _ := vips.Colourspace(im, vips.InterpretationBW, nil)
back, _ := vips.Colourspace(gray, vips.InterpretationSrgb, nil)
vips.Arrayjoin([]*vips.Image{im, back}, &vips.ArrayjoinOptions{
    Across: vips.Ptr(2),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				gray, err := vips.Colourspace(im, vips.InterpretationBW, nil)
				if err != nil {
					return nil, err
				}
				defer gray.Close()
				back, err := vips.Colourspace(gray, vips.InterpretationSrgb, nil)
				if err != nil {
					return nil, err
				}
				defer back.Close()
				return vips.Arrayjoin([]*vips.Image{im, back}, &vips.ArrayjoinOptions{
					Across: vips.Ptr(2),
				})
			},
		},
		{
			Name: "Similarity", Govips: "Similarity",
			Code: `vips.Similarity(im, &vips.SimilarityOptions{
    Scale: vips.Ptr(0.6), Angle: vips.Ptr(20.0),
})`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Similarity(im, &vips.SimilarityOptions{
					Scale: vips.Ptr(0.6), Angle: vips.Ptr(20.0),
				})
			},
		},
		{
			Name: "Affine, shear", Govips: "(not in govips)",
			Code: `vips.Affine(im, []float64{1, 0.3, 0, 1}, nil)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				return vips.Affine(im, []float64{1, 0.3, 0, 1}, nil)
			},
		},
		{
			Name: "Falsecolour", Govips: "(not in govips)",
			Code: `gray, _ := vips.Colourspace(im, vips.InterpretationBW, nil)
vips.Falsecolour(gray)`,
			Run: func(im *vips.Image) (*vips.Image, error) {
				gray, err := vips.Colourspace(im, vips.InterpretationBW, nil)
				if err != nil {
					return nil, err
				}
				defer gray.Close()
				return vips.Falsecolour(gray)
			},
		},
	}
}

// matrix3x3 builds a small double matrix, which is how libvips takes
// convolution and recombination kernels.
func matrix3x3(values []float64) (*vips.Image, error) {
	black, err := vips.Black(3, 3, nil)
	if err != nil {
		return nil, err
	}
	defer black.Close()

	m, err := vips.Cast(black, vips.BandFormatDouble, nil)
	if err != nil {
		return nil, err
	}
	for i, v := range values {
		next, err := vips.DrawRect(m, []float64{v}, i%3, i/3, 1, 1,
			&vips.DrawRectOptions{Fill: vips.Ptr(true)})
		m.Close()
		if err != nil {
			return nil, err
		}
		m = next
	}
	return m, nil
}

func main() {
	width := flag.Int("width", 600, "width to work at, in pixels")
	ext := flag.String("format", ".jpg", "output format, by extension")
	quality := flag.Int("q", 88, "encoder quality")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: %s [flags] <source-image> <output-dir>\n\nflags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	srcPath, outDir := flag.Arg(0), flag.Arg(1)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	full, err := vips.LoadFile(srcPath)
	if err != nil {
		log.Fatal(err)
	}
	defer full.Close()

	// Work at a manageable size so the page stays light.
	src, err := vips.ThumbnailImage(full, *width, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()

	if err := vips.SaveFile(src, filepath.Join(outDir, "00-source"+*ext),
		vips.In("Q", *quality)); err != nil {
		log.Fatal(err)
	}

	list := demos()
	ok, failed := 0, 0
	for i := range list {
		d := &list[i]
		name := fmt.Sprintf("%02d-%s", i+1, strings.NewReplacer(
			" ", "-", ",", "", "(", "", ")", "").Replace(d.Name))
		d.File = name + *ext

		start := time.Now()
		res, err := d.Run(src)
		if err != nil {
			d.Err, d.File = err.Error(), ""
			failed++
			fmt.Printf("  %-28s FAILED  %v\n", d.Name, err)
			continue
		}
		d.Duration = time.Since(start).Round(time.Microsecond).String()

		// Some operations return formats a JPEG cannot hold; normalise first.
		flat, err := forJPEG(res)
		if err != nil {
			res.Close()
			d.Err, d.File = err.Error(), ""
			failed++
			continue
		}

		if err := vips.SaveFile(flat, filepath.Join(outDir, d.File), vips.In("Q", *quality)); err != nil {
			d.Err, d.File = err.Error(), ""
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
		fmt.Printf("  %-28s %4dx%-4d  %8s  %s\n", d.Name, d.Width, d.Height, d.Duration, d.File)

		flat.Close()
		res.Close()
	}

	sort.SliceStable(list, func(i, j int) bool { return list[i].Err == "" && list[j].Err != "" })

	if err := writeIndex(outDir, srcPath, "00-source"+*ext, list, ok, failed); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d succeeded, %d failed. Open %s\n",
		ok, failed, filepath.Join(outDir, "index.html"))
}

// forJPEG coerces whatever an operation returned into something JPEG accepts:
// eight bits per sample, one or three bands, no alpha.
func forJPEG(im *vips.Image) (*vips.Image, error) {
	cur, owned := im, false
	replace := func(next *vips.Image, err error) error {
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
		if err := replace(vips.Flatten(cur, &vips.FlattenOptions{
			Background: vips.Ptr([]float64{255, 255, 255}),
		})); err != nil {
			return nil, err
		}
	}
	if cur.Bands() > 3 {
		if err := replace(vips.ExtractBand(cur, 0, &vips.ExtractBandOptions{
			N: vips.Ptr(3),
		})); err != nil {
			return nil, err
		}
	}
	// Scale maps any numeric range onto 0-255, which is what makes float
	// results such as canny or the histograms viewable at all.
	if err := replace(vips.Scale(cur, nil)); err != nil {
		if err := replace(vips.Cast(cur, vips.BandFormatUchar, nil)); err != nil {
			return nil, err
		}
	}
	if !owned {
		return vips.Copy(cur, nil)
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
          grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); }
  figure { margin: 0; border: 1px solid color-mix(in srgb, currentColor 15%, transparent);
           border-radius: 10px; overflow: hidden; }
  figure img { display: block; width: 100%; height: auto; background: #8883; }
  figcaption { padding: .7rem .85rem .85rem; }
  .op { font-weight: 600; }
  .meta { opacity: .6; font-size: .82rem; margin-top: .15rem; }
  .govips { font-size: .82rem; opacity: .75; margin-top: .3rem; }
  code { font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
         display: block; margin-top: .5rem; padding: .55rem .65rem; border-radius: 6px;
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
    <img src="{{.SourceFile}}" alt="source">
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

func writeIndex(dir, source, sourceFile string, list []demo, ok, failed int) error {
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
		Demos        []demo
		OK, Failed   int
		Version, Ops string
		Source       string
		SourceFile   string
	}{
		Demos:      list,
		OK:         ok,
		Failed:     failed,
		Version:    vips.Version(),
		Ops:        fmt.Sprint(len(vips.Operations())),
		Source:     filepath.Base(source),
		SourceFile: sourceFile,
	})
}
