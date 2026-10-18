package oci

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// An OCI image with no layers is valid and passes validation, so
// UnCompressOCIArtifact has to reject it with an error instead of
// panicking when it reads the first layer.
func TestUnCompressOCIArtifactZeroLayer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty-artifact.tar")
	ref, err := name.ParseReference("example.com/test/empty:latest")
	if err != nil {
		t.Fatal(err)
	}
	if err := tarball.WriteToFile(src, ref, empty.Image); err != nil {
		t.Fatalf("write empty image tarball: %v", err)
	}

	dest := filepath.Join(dir, "out-empty")
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on zero-layer image: %v", r)
			}
		}()
		err := UnCompressOCIArtifact(src, dest)
		if err == nil {
			t.Fatalf("expected an error for a zero-layer image, got nil (false success)")
		}
	}()
}

func TestUnCompressOCIArtifactOneLayer(t *testing.T) {
	dir := t.TempDir()

	// Build a one-layer image whose layer is a tar containing hello.txt.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	body := []byte("hello oci")
	if err := tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layer, err := tarball.LayerFromReader(bytes.NewReader(tarBuf.Bytes()))
	if err != nil {
		t.Fatalf("layer from reader: %v", err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("append layer: %v", err)
	}

	src := filepath.Join(dir, "one-layer.tar")
	ref, err := name.ParseReference("example.com/test/one:latest")
	if err != nil {
		t.Fatal(err)
	}
	if err := tarball.WriteToFile(src, ref, img); err != nil {
		t.Fatalf("write one-layer tarball: %v", err)
	}

	dest := filepath.Join(dir, "out-one")
	if err := UnCompressOCIArtifact(src, dest); err != nil {
		t.Fatalf("one-layer uncompress failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil {
		t.Fatalf("hello.txt not extracted: %v", err)
	}
	if string(got) != "hello oci" {
		t.Fatalf("unexpected hello.txt content: %q", string(got))
	}
}
