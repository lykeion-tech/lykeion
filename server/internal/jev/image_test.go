package jev

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFitImageBringsSidesIntoRange(t *testing.T) {
	for _, c := range []struct{ w, h int }{{3000, 200}, {100, 50}, {4000, 3000}, {500, 400}} {
		im, err := FitImage(pngOf(t, c.w, c.h), "png")
		if err != nil {
			t.Fatalf("%dx%d: %v", c.w, c.h, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(im.Data))
		if err != nil || format != "png" || im.Format != "png" {
			t.Fatalf("%dx%d: %s %v", c.w, c.h, format, err)
		}
		px := cfg.Width * cfg.Height
		if cfg.Width < MinImageSide || cfg.Width > MaxImageSide || cfg.Height < MinImageSide || cfg.Height > MaxImageSide || px < 78400 || px > 2458624 {
			t.Fatalf("%dx%d became %dx%d", c.w, c.h, cfg.Width, cfg.Height)
		}
		if c.w == 500 && (cfg.Width != 500 || cfg.Height != 400) {
			t.Fatalf("an image in range was resized to %dx%d", cfg.Width, cfg.Height)
		}
	}
}

func TestFitImageRefusesOtherFormats(t *testing.T) {
	if _, err := FitImage(pngOf(t, 400, 400), "jpeg"); err == nil {
		t.Fatal("a PNG passed as a JPEG")
	}
	if _, err := FitImage([]byte("not an image"), "png"); err == nil {
		t.Fatal("garbage decoded")
	}
}

func TestImagesOnlyGoWithTheirHeader(t *testing.T) {
	type seen struct {
		header string
		assets map[string]map[string]string
		state  string
	}
	var got []seen
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State  string
			Assets map[string]map[string]string
		}
		json.NewDecoder(r.Body).Decode(&body)
		got = append(got, seen{r.Header.Get("TypeSafe-Danger-Zone-Images"), body.Assets, body.State})
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": map[string]any{"q": map[string]any{"type": "noul", "noul": 0.8}}})
	}))
	defer s.Close()
	c := &Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"}
	ctx := context.Background()
	if _, err := c.Noul(ctx, "s", map[string]string{"q": "yes?"}); err != nil {
		t.Fatal(err)
	}
	ref := "4f6c2a52-6a0e-4d0b-9a35-6a1f0c1e2d3b"
	data := pngOf(t, 300, 300)
	if _, err := c.NoulImages(ctx, "figure "+ref, map[string]string{"q": "yes?"}, map[string]Image{ref: {Format: "png", Data: data}}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].header != "" || got[0].assets != nil {
		t.Fatalf("plain request %+v", got)
	}
	a := got[1].assets[ref]
	if got[1].header != "true" || a["type"] != "image" || a["format"] != "png" || a["data_base64"] != base64.StdEncoding.EncodeToString(data) ||
		!strings.Contains(got[1].state, ref) {
		t.Fatalf("image request %+v", got[1])
	}
	if _, err := c.NoulImages(ctx, "no reference", map[string]string{"q": "yes?"}, map[string]Image{ref: {Format: "png", Data: data}}); err == nil || len(got) != 2 {
		t.Fatal("an image the state does not name was sent")
	}
}
