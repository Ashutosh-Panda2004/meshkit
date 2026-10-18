package kompose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func concurrentCompose(svc string) DockerComposeFile {
	return DockerComposeFile(fmt.Sprintf("version: \"3.8\"\nservices:\n  %s:\n    image: nginx:1.25\n", svc))
}

// Conversions running at the same time must stay independent: every caller
// has to get the manifests generated from its own compose file.
func TestConvertConcurrentConversions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Convert assumes ~/.meshery already exists (the server creates it at startup).
	if err := os.MkdirAll(filepath.Join(home, ".meshery"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Sequential control.
	control, err := Convert(concurrentCompose("controlsvc"))
	if err != nil {
		t.Fatalf("control convert failed: %v", err)
	}
	if !strings.Contains(control, "controlsvc") {
		t.Fatalf("control output wrong: %q", control)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := fmt.Sprintf("svc%02d", i)
			out, err := Convert(concurrentCompose(svc))
			errs[i] = err
			results[i] = out
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		svc := fmt.Sprintf("svc%02d", i)
		switch {
		case errs[i] != nil:
			t.Errorf("goroutine %d (%s): ERROR %v", i, svc, errs[i])
		case !strings.Contains(results[i], svc):
			t.Errorf("goroutine %d (%s): WRONG OUTPUT:\n%s", i, svc, results[i])
		}
	}
}
