package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderHTML(t *testing.T) {
	result := renderHTML("Курс <Go>", []textSection{{Title: "Основы", Lessons: []textLesson{{Title: "Урок", URL: "https://stepik.org/lesson/1/step/1", Content: []string{"<p>Привет</p><pre><code>fmt.Println(1)</code></pre>"}}}}})
	for _, value := range []string{"Курс &lt;Go&gt;", `id="section-1"`, `id="lesson-1-1"`, "fmt.Println(1)", "Содержание"} {
		if !strings.Contains(result, value) {
			t.Fatalf("HTML does not contain %q", value)
		}
	}
}

func TestLocalizeHTMLNoExternal(t *testing.T) {
	folder := t.TempDir()
	input := `<p>Текст</p><script>alert(1)</script><pre>code</pre><img src="data:image/png;base64,foo" alt="inline">`
	actual, n, err := localizeHTML(input, folder)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("localized = %d, want 0", n)
	}
	if strings.Contains(actual, "alert") || !strings.Contains(actual, "<pre>code</pre>") {
		t.Fatalf("unexpected HTML: %q", actual)
	}
	files, err := os.ReadDir(folder)
	if err != nil || len(files) != 0 {
		t.Fatalf("unexpected files: %v, %v", files, err)
	}
}

func TestSafeName(t *testing.T) {
	if safeName("A/B:C") != "A B C" {
		t.Fatal("filename not sanitized")
	}
	if filepath.Base(filepath.Join("output", safeName("Test"))) != "Test" {
		t.Fatal("path issue")
	}
}

func TestFindBrowserOverride(t *testing.T) {
	browser := filepath.Join(t.TempDir(), "Google Chrome")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROME_PATH", browser)
	got, err := findBrowser()
	if err != nil || got != browser {
		t.Fatalf("findBrowser() = %q, %v; want %q", got, err, browser)
	}
}

func TestFindBrowserBadOverride(t *testing.T) {
	t.Setenv("CHROME_PATH", filepath.Join(t.TempDir(), "missing-browser"))
	_, err := findBrowser()
	if err == nil || !strings.Contains(err.Error(), "CHROME_PATH") {
		t.Fatalf("expected CHROME_PATH error, got %v", err)
	}
}
