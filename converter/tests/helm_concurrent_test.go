package converter_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/meshery/meshkit/converter"
)

func concurrentPattern(marker string) string {
	return fmt.Sprintf(`name: race-demo
version: 0.1.0
components:
  - displayName: %s
    component:
      kind: ConfigMap
      version: v1
    configuration:
      data:
        marker: %s
`, marker, marker)
}

func extractPackagedManifest(chartData []byte) (string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(chartData))
	if err != nil {
		return "", err
	}
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(hdr.Name, "templates/manifest.yaml") {
			buf := new(bytes.Buffer)
			if _, err := io.Copy(buf, tr); err != nil {
				return "", err
			}
			return buf.String(), nil
		}
	}
	return "", fmt.Errorf("manifest.yaml not found")
}

// Conversions of designs that share a chart name and version must not
// interfere with each other: every caller has to receive its own chart.
func TestHelmConverter_ConcurrentSameNameVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	hc := &converter.HelmConverter{}

	// Sequential control: a single conversion returns its own manifest.
	controlChart, err := hc.Convert(concurrentPattern("control-marker"))
	if err != nil {
		t.Fatalf("control convert failed: %v", err)
	}
	controlManifest, err := extractPackagedManifest([]byte(controlChart))
	if err != nil {
		t.Fatalf("control extract failed: %v", err)
	}
	if !strings.Contains(controlManifest, "control-marker") {
		t.Fatalf("control manifest wrong: %q", controlManifest)
	}

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	manifests := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			marker := fmt.Sprintf("marker-%02d", i)
			chartData, err := hc.Convert(concurrentPattern(marker))
			if err != nil {
				errs[i] = err
				return
			}
			m, err := extractPackagedManifest([]byte(chartData))
			if err != nil {
				errs[i] = err
				return
			}
			manifests[i] = m
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		marker := fmt.Sprintf("marker-%02d", i)
		switch {
		case errs[i] != nil:
			t.Errorf("goroutine %d (%s): ERROR %v", i, marker, errs[i])
		case !strings.Contains(manifests[i], marker):
			t.Errorf("goroutine %d (%s): WRONG MANIFEST returned:\n%s", i, marker, manifests[i])
		}
	}
}
