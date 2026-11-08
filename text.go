package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Text export keeps the author's HTML markup while removing active and non-printable content.
// All downloaded images are re-pointed to local files before offline PDF generation.
var (
	imgTagRE       = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	srcAttrRE      = regexp.MustCompile(`(?i)\ssrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	singleTagRE    = regexp.MustCompile(`(?is)<(?:input|source|track)\b[^>]*>`)
	unwantedPairRE = regexp.MustCompile(`(?is)<(?:script|iframe|form|video|audio|button|textarea|select)\b[^>]*>.*?</(?:script|iframe|form|video|audio|button|textarea|select)\s*>`)
	eventAttrRE    = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
	srcsetAttrRE   = regexp.MustCompile(`(?i)\s+srcset\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
)

type textSection struct {
	Title   string
	Lessons []textLesson
}
type textLesson struct {
	Title   string
	URL     string
	Content []string
}

func runText(course Course, root string) error {
	assetsDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return err
	}
	var sections []textSection
	failed := 0
	texts := 0
	for si, sectionID := range course.Sections {
		section, err := getSection(sectionID)
		if err != nil {
			fmt.Printf("! section %d: %v\n", sectionID, err)
			failed++
			continue
		}
		entry := textSection{Title: section.Title}
		fmt.Printf("[%d/%d] %s\n", si+1, len(course.Sections), section.Title)
		for ui, unitID := range section.Units {
			unit, err := getUnit(unitID)
			if err != nil {
				fmt.Printf("  ! unit %d: %v\n", unitID, err)
				failed++
				continue
			}
			lesson, err := getLesson(unit.Lesson)
			if err != nil {
				fmt.Printf("  ! lesson %d: %v\n", unit.Lesson, err)
				failed++
				continue
			}
			item := textLesson{Title: lesson.Title, URL: fmt.Sprintf("https://stepik.org/lesson/%d/step/1?unit=%d", lesson.ID, unit.ID)}
			fmt.Printf("  [%d/%d] %s\n", ui+1, len(section.Units), lesson.Title)
			for _, stepID := range lesson.Steps {
				step, err := getStep(stepID)
				if err != nil {
					fmt.Printf("    ! step %d: %v\n", stepID, err)
					failed++
					continue
				}
				if step.Block.Name != "text" || strings.TrimSpace(step.Block.Text) == "" {
					continue
				}
				content, n, err := localizeHTML(step.Block.Text, assetsDir)
				if err != nil {
					fmt.Printf("    ! изображения step %d: %v\n", stepID, err)
					failed++
				}
				item.Content = append(item.Content, content)
				texts++
				fmt.Printf("    ✓ теория, изображений: %d\n", n)
			}
			if len(item.Content) > 0 {
				entry.Lessons = append(entry.Lessons, item)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if len(entry.Lessons) > 0 {
			sections = append(sections, entry)
		}
	}
	if texts == 0 {
		return fmt.Errorf("не найдено ни одного текстового шага (ошибок API: %d); проверьте cookies и содержимое курса", failed)
	}
	htmlFile := filepath.Join(root, "course.html")
	if err := os.WriteFile(htmlFile, []byte(renderHTML(course.Title, sections)), 0644); err != nil {
		return err
	}
	fmt.Printf("HTML: %s\nТекстовых шагов: %d; ошибок: %d\n", htmlFile, texts, failed)
	pdfFile := filepath.Join(root, "course.pdf")
	if err := printPDF(htmlFile, pdfFile); err != nil {
		fmt.Printf("PDF не создан: %v\nHTML и локальные изображения сохранены.\n", err)
		return nil // HTML is still a useful output, not a total failure.
	}
	fmt.Printf("PDF: %s\n", pdfFile)
	return nil
}

func localizeHTML(raw, assetsDir string) (string, int, error) {
	raw = unwantedPairRE.ReplaceAllString(raw, "")
	raw = singleTagRE.ReplaceAllString(raw, "")
	raw = eventAttrRE.ReplaceAllString(raw, "")
	// Source URLs are changed only in <img> tags; other links remain clickable.
	count := 0
	var firstErr error
	raw = imgTagRE.ReplaceAllStringFunc(raw, func(tag string) string {
		match := srcAttrRE.FindStringSubmatchIndex(tag)
		if match == nil {
			return tag
		}
		var source string
		for i := 2; i+1 < len(match); i += 2 {
			if match[i] != -1 {
				source = html.UnescapeString(tag[match[i]:match[i+1]])
				break
			}
		}
		asset, err := saveImage(source, assetsDir)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return tag
		}
		if asset == "" {
			return tag
		}
		// Remove srcset so a browser does not make an online request instead of using the local src.
		tag = srcsetAttrRE.ReplaceAllString(tag, "")
		match = srcAttrRE.FindStringSubmatchIndex(tag)
		tag = tag[:match[0]] + ` src="assets/` + html.EscapeString(asset) + `"` + tag[match[1]:]
		count++
		return tag
	})
	return raw, count, firstErr
}

func saveImage(source, folder string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" || strings.HasPrefix(source, "data:") {
		return "", nil
	}
	if strings.HasPrefix(source, "//") {
		source = "https:" + source
	}
	u, err := url.Parse(source)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" && u.Host == "" {
		u, err = url.Parse("https://stepik.org" + "/" + strings.TrimPrefix(source, "/"))
		if err != nil {
			return "", err
		}
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("недопустимый URL изображения %q", source)
	}
	// Reject localhost, internal network URLs and non-standard ports to avoid local-network access.
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || u.Port() != "" || host == "127.0.0.1" || host == "::1" {
		return "", fmt.Errorf("недопустимый адрес изображения")
	}
	hash := sha256.Sum256([]byte(u.String()))
	key := hex.EncodeToString(hash[:12])
	ext := strings.ToLower(filepath.Ext(u.Path))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" && ext != ".svg" {
		ext = ".img"
	}
	filename := key + ext
	target := filepath.Join(folder, filename)
	if stat, err := os.Stat(target); err == nil && stat.Size() > 0 {
		return filename, nil
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 StepikOffline/1.0")
	req.Header.Set("Referer", "https://stepik.org/")
	if cookieHeader != "" && (host == "stepik.org" || strings.HasSuffix(host, ".stepik.org")) {
		req.Header.Set("Cookie", cookieHeader)
	}
	// Do not forward session cookies to redirects on unrelated hosts.
	imgClient := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("слишком много редиректов")
		}
		req.Header.Del("Cookie")
		return nil
	}}
	resp, err := imgClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("изображение %s: HTTP %d", u.Host, resp.StatusCode)
	}
	if resp.ContentLength > 25<<20 {
		return "", fmt.Errorf("изображение слишком большое")
	}
	f, err := os.Create(target + ".part")
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, (25<<20)+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n > 25<<20 {
		os.Remove(target + ".part")
		return "", fmt.Errorf("ошибка загрузки изображения: %v %v", copyErr, closeErr)
	}
	if err := os.Rename(target+".part", target); err != nil {
		return "", err
	}
	return filename, nil
}

func renderHTML(title string, sections []textSection) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title><style>
@page {size:A4; margin:18mm 16mm 20mm} *{box-sizing:border-box}
body{font:11.5pt/1.55 Arial,"DejaVu Sans",sans-serif;color:#20242a;max-width:900px;margin:0 auto;padding:12px}
h1{font-size:26pt;margin:2em 0 0.5em} h2{font-size:19pt;margin:1.7em 0 0.5em;break-after:avoid} h3{font-size:15pt;break-after:avoid}
h4,h5{break-after:avoid} p,li{overflow-wrap:anywhere} img{max-width:100%;height:auto;break-inside:avoid}
pre{font:9pt/1.45 Consolas,Monaco,monospace;background:#f4f6f8;padding:12px;white-space:pre-wrap;overflow-wrap:anywhere;border-radius:5px}
code{font-family:Consolas,Monaco,monospace;background:#f4f6f8;padding:1px 3px} pre code{padding:0}
blockquote{border-left:3px solid #8b98a8;padding:4px 14px;margin:16px 0} table{border-collapse:collapse;width:100%;font-size:10pt} td,th{border:1px solid #bbb;padding:6px;vertical-align:top}
a{color:#2159a5;overflow-wrap:anywhere} .cover{min-height:65vh;display:flex;align-items:center;justify-content:center;text-align:center}
.toc{break-after:page}.toc a{text-decoration:none}.toc li{margin:4px 0}.module{break-before:page}.lesson{margin-top:22px}
.step + .step{border-top:1px solid #e5e8ef;padding-top:14px;margin-top:18px}
@media print { body{max-width:none;padding:0} .cover{break-after:page} h2,h3{break-after:avoid} }
</style></head><body>`)
	b.WriteString(`<div class="cover"><h1>` + html.EscapeString(title) + `</h1></div><nav class="toc"><h2>Содержание</h2><ol>`)
	for i, s := range sections {
		b.WriteString(fmt.Sprintf(`<li><a href="#section-%d">%s</a><ol>`, i+1, html.EscapeString(s.Title)))
		for j, l := range s.Lessons {
			b.WriteString(fmt.Sprintf(`<li><a href="#lesson-%d-%d">%s</a></li>`, i+1, j+1, html.EscapeString(l.Title)))
		}
		b.WriteString(`</ol></li>`)
	}
	b.WriteString(`</ol></nav>`)
	for i, s := range sections {
		b.WriteString(fmt.Sprintf(`<section class="module" id="section-%d"><h2>%d. %s</h2>`, i+1, i+1, html.EscapeString(s.Title)))
		for j, l := range s.Lessons {
			b.WriteString(fmt.Sprintf(`<article class="lesson" id="lesson-%d-%d"><h3>%d.%d %s</h3><p><small><a href="%s">Оригинал на Stepik</a></small></p>`, i+1, j+1, i+1, j+1, html.EscapeString(l.Title), html.EscapeString(l.URL)))
			for _, content := range l.Content {
				b.WriteString(`<div class="step">`)
				b.WriteString(content)
				b.WriteString(`</div>`)
			}
			b.WriteString(`</article>`)
		}
		b.WriteString(`</section>`)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// findBrowser first respects an explicit override, then checks standard installation
// directories (Chrome on macOS lives in an .app bundle and is not normally in PATH).
func findBrowser() (string, error) {
	if override := strings.TrimSpace(os.Getenv("CHROME_PATH")); override != "" {
		path, err := browserExecutable(override)
		if err != nil {
			return "", fmt.Errorf("CHROME_PATH=%q: %w", override, err)
		}
		return path, nil
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		apps := []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Chromium.app/Contents/MacOS/Chromium",
			"Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
		for _, base := range []string{"/Applications", "/System/Volumes/Data/Applications", filepath.Join(os.Getenv("HOME"), "Applications")} {
			for _, app := range apps {
				candidates = append(candidates, filepath.Join(base, app))
			}
		}
		candidates = append(candidates, "/opt/homebrew/bin/chromium", "/usr/local/bin/chromium")
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"),
			)
		}
	}
	// Also support Linux installations and browsers installed through package managers.
	candidates = append(candidates, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "microsoft-edge", "msedge")
	for _, candidate := range candidates {
		if path, err := browserExecutable(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("Chrome/Chromium/Edge не найден. Установите браузер или укажите путь к исполняемому файлу через CHROME_PATH")
}

func browserExecutable(candidate string) (string, error) {
	if !filepath.IsAbs(candidate) && !strings.ContainsAny(candidate, `/\`) {
		return exec.LookPath(candidate)
	}
	path, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if stat.IsDir() {
		return "", fmt.Errorf("это директория, а не исполняемый файл")
	}
	if runtime.GOOS != "windows" && stat.Mode()&0111 == 0 {
		return "", fmt.Errorf("нет прав на выполнение")
	}
	return path, nil
}

func printPDF(htmlFile, pdfFile string) error {
	browser, err := findBrowser()
	if err != nil {
		return err
	}
	absHTML, err := filepath.Abs(htmlFile)
	if err != nil {
		return err
	}
	absPDF, err := filepath.Abs(pdfFile)
	if err != nil {
		return err
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absHTML)}).String()
	if runtime.GOOS == "windows" {
		uri = "file:///" + strings.ReplaceAll(filepath.ToSlash(absHTML), " ", "%20")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--headless", "--disable-gpu", "--no-pdf-header-footer", "--allow-file-access-from-files", "--print-to-pdf="+absPDF, uri)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("время генерации PDF истекло: %w", ctx.Err())
		}

		return fmt.Errorf("Chrome: %w: %s", err, strings.TrimSpace(string(output)))
	}
	stat, err := os.Stat(absPDF)
	if err != nil || stat.Size() == 0 {
		return fmt.Errorf("Chrome не создал PDF: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
