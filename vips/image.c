// The image handle itself: releasing a reference and reading the dimensions
// that live directly on the struct.

#include "vipsx.h"

void vipsx_image_unref(VipsImage *image) { g_object_unref(image); }

int vipsx_image_width(VipsImage *image) { return image->Xsize; }

int vipsx_image_height(VipsImage *image) { return image->Ysize; }

int vipsx_image_bands(VipsImage *image) { return image->Bands; }
