#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <libheif/heif.h>
#include <libheif/heif_security.h>
#include <libheif/heif_sequences.h>
static uint8_t* pixels;
static int width,height;
void preview_release(void) {free(pixels); pixels=0; width=height=0;}
int preview_width(void){return width;}
int preview_height(void){return height;}
uint8_t* preview_pixels(void){return pixels;}
int preview_decode(const uint8_t* data, int length) {
  heif_context* ctx=NULL; heif_image_handle* handle=NULL; heif_image* image=NULL; int ok=0,stride=0;
  preview_release();
  if(!data || length<12 || length>8*1024*1024) return 0;
  ctx=heif_context_alloc(); if(!ctx) goto done;
  heif_security_limits* limits=heif_context_get_security_limits(ctx);
  if(!limits) goto done;
  limits->max_image_size_pixels=4194304; limits->max_number_of_tiles=256; limits->max_items=512;
  limits->max_color_profile_size=1024*1024; limits->max_memory_block_size=32*1024*1024;
  limits->max_total_memory=96*1024*1024; limits->max_sequence_frames=1;
  if(heif_context_read_from_memory_without_copy(ctx,data,length,NULL).code) goto done;
  if(heif_context_has_sequence(ctx) || heif_context_number_of_sequence_tracks(ctx) || heif_context_get_number_of_top_level_images(ctx)!=1) goto done;
  if(heif_context_get_primary_image_handle(ctx,&handle).code || !handle) goto done;
  int w=heif_image_handle_get_width(handle),h=heif_image_handle_get_height(handle);
  if(w<=0 || h<=0 || w>4096 || h>4096 || (uint64_t)w*h>4194304) goto done;
  if(heif_decode_image(handle,&image,heif_colorspace_RGB,heif_chroma_interleaved_RGBA,NULL).code || !image) goto done;
  int iw=heif_image_get_width(image,heif_channel_interleaved),ih=heif_image_get_height(image,heif_channel_interleaved);
  if(iw!=w || ih!=h) goto done;
  const uint8_t* data_out=heif_image_get_plane_readonly(image,heif_channel_interleaved,&stride);
  if(!data_out || stride<w*4 || stride>32*1024*1024 || (uint64_t)stride*h>64*1024*1024) goto done;
  pixels=malloc((size_t)w*h*4); if(!pixels) goto done;
  for(int y=0;y<h;y++) memcpy(pixels+(size_t)y*w*4,data_out+(size_t)y*stride,(size_t)w*4);
  width=w;height=h;ok=1;
 done: if(image)heif_image_release(image); if(handle)heif_image_handle_release(handle); if(ctx)heif_context_free(ctx); if(!ok) preview_release(); return ok;
}
