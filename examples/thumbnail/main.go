// Command thumbnail shows the whole of vipsx in one file: load an image,
// resize it, and write it out in another format.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/kirklin/vipsx/vips"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s <input> <output>", os.Args[0])
	}
	in, out := os.Args[1], os.Args[2]

	fmt.Printf("libvips %s, %d operations available\n",
		vips.Version(), len(vips.Operations()))

	src, err := vips.LoadFile(in)
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()
	fmt.Printf("loaded %dx%d, %d bands\n", src.Width(), src.Height(), src.Bands())

	outs, err := vips.Call("thumbnail_image",
		vips.In("in", src),
		vips.In("width", 640),
		// An explicit zero is an explicit zero: this really is
		// VipsInteresting 0, "none", not a request for the default.
		vips.In("crop", 0),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer outs.Close()

	thumb, err := outs.Image("out")
	if err != nil {
		log.Fatal(err)
	}

	if err := vips.SaveFile(thumb, out); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s at %dx%d\n", out, thumb.Width(), thumb.Height())
}
