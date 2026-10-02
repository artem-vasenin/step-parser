package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	configFile = "config.json"
	apiBase    = "https://stepik.org/api"
)

type Config struct {
	CourseID    int    `json:"course_id"`
	CookiesFile string `json:"cookies_file"`
	Quality     string `json:"quality"`
}

var config Config
var cookieHeader string

var client = &http.Client{
	Timeout: 60 * time.Second,
}

// ---------- Stepik API structures ----------

type CourseResponse struct {
	Courses []Course `json:"courses"`
}

type Course struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Sections []int  `json:"sections"`
}

type SectionResponse struct {
	Sections []Section `json:"sections"`
}

type Section struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Position int    `json:"position"`
	Units    []int  `json:"units"`
}

type UnitResponse struct {
	Units []Unit `json:"units"`
}

type Unit struct {
	ID       int `json:"id"`
	Lesson   int `json:"lesson"`
	Position int `json:"position"`
}

type LessonResponse struct {
	Lessons []Lesson `json:"lessons"`
}

type Lesson struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Steps []int  `json:"steps"`
}

type StepResponse struct {
	Steps []Step `json:"steps"`
}

type Step struct {
	ID       int       `json:"id"`
	Position int       `json:"position"`
	Block    StepBlock `json:"block"`
}

type StepBlock struct {
	Name  string     `json:"name"`
	Video *VideoInfo `json:"video"`
}

type VideoInfo struct {
	ID       int        `json:"id"`
	Duration int        `json:"duration"`
	Filename string     `json:"filename"`
	URLs     []VideoURL `json:"urls"`
}

type VideoURL struct {
	Quality string `json:"quality"`
	URL     string `json:"url"`
}

// ---------- HTTP ----------

func getJSON(url string, dst any) error {
	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 StepikOffline/1.0")
		req.Header.Set("Accept", "application/json")
		if cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()

			lastErr = fmt.Errorf(
				"HTTP %d: %s",
				resp.StatusCode,
				strings.TrimSpace(string(body)),
			)

			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		err = json.NewDecoder(resp.Body).Decode(dst)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		return nil
	}

	return lastErr
}

// ---------- filenames ----------

var invalidFilenameChars = regexp.MustCompile(`[/:*?"<>|]`)

func safeName(name string) string {
	name = strings.TrimSpace(name)
	name = invalidFilenameChars.ReplaceAllString(name, " ")
	name = strings.Join(strings.Fields(name), " ")

	if name == "" {
		return "Без названия"
	}

	return name
}

func pad(number, total int) string {
	width := len(strconv.Itoa(total))

	if width < 2 {
		width = 2
	}

	return fmt.Sprintf("%0*d", width, number)
}

// ---------- API helpers ----------

func getCourse(id int) (Course, error) {
	var result CourseResponse

	err := getJSON(
		fmt.Sprintf("%s/courses/%d", apiBase, id),
		&result,
	)
	if err != nil {
		return Course{}, err
	}

	if len(result.Courses) == 0 {
		return Course{}, errors.New("курс не найден")
	}

	return result.Courses[0], nil
}

func getSection(id int) (Section, error) {
	var result SectionResponse

	err := getJSON(
		fmt.Sprintf("%s/sections/%d", apiBase, id),
		&result,
	)
	if err != nil {
		return Section{}, err
	}

	if len(result.Sections) == 0 {
		return Section{}, errors.New("секция не найдена")
	}

	return result.Sections[0], nil
}

func getUnit(id int) (Unit, error) {
	var result UnitResponse

	err := getJSON(
		fmt.Sprintf("%s/units/%d", apiBase, id),
		&result,
	)
	if err != nil {
		return Unit{}, err
	}

	if len(result.Units) == 0 {
		return Unit{}, errors.New("unit не найден")
	}

	return result.Units[0], nil
}

func getLesson(id int) (Lesson, error) {
	var result LessonResponse

	err := getJSON(
		fmt.Sprintf("%s/lessons/%d", apiBase, id),
		&result,
	)
	if err != nil {
		return Lesson{}, err
	}

	if len(result.Lessons) == 0 {
		return Lesson{}, errors.New("урок не найден")
	}

	return result.Lessons[0], nil
}

func getStep(id int) (Step, error) {
	var result StepResponse

	err := getJSON(
		fmt.Sprintf("%s/steps/%d", apiBase, id),
		&result,
	)
	if err != nil {
		return Step{}, err
	}

	if len(result.Steps) == 0 {
		return Step{}, errors.New("step не найден")
	}

	return result.Steps[0], nil
}

// ---------- video ----------

func chooseVideoURL(video VideoInfo) (string, string, error) {
	// Сначала пытаемся взять 1080.
	for _, item := range video.URLs {
		if item.Quality == config.Quality {
			return item.URL, item.Quality, nil
		}
	}

	// Если 1080 отсутствует — максимальное доступное качество.
	bestURL := ""
	bestQuality := ""
	bestNumber := -1

	for _, item := range video.URLs {
		q, err := strconv.Atoi(item.Quality)
		if err != nil {
			continue
		}

		if q > bestNumber {
			bestNumber = q
			bestQuality = item.Quality
			bestURL = item.URL
		}
	}

	if bestURL == "" {
		return "", "", errors.New("URL видео отсутствует")
	}

	return bestURL, bestQuality, nil
}

func downloadFile(url, target string) error {
	// Уже существует — не скачиваем повторно.
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		fmt.Printf("      ✓ уже скачано: %s\n", filepath.Base(target))
		return nil
	}

	tmp := target + ".part"

	// Если остался недокачанный файл, пока начинаем заново.
	_ = os.Remove(tmp)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 StepikOffline/1.0")
	req.Header.Set("Referer", "https://stepik.org/")
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("сервер вернул HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	written, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()

	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}

	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}

	if resp.ContentLength > 0 && written != resp.ContentLength {
		_ = os.Remove(tmp)

		return fmt.Errorf(
			"получено %d байт, ожидалось %d",
			written,
			resp.ContentLength,
		)
	}

	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return nil
}

// ---------- main ----------

func loadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("не удалось прочитать %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("некорректный JSON в %s: %w", path, err)
	}

	if config.CourseID <= 0 {
		return errors.New("course_id должен быть положительным числом")
	}
	config.Quality = strings.TrimSpace(config.Quality)
	if config.Quality == "" {
		return errors.New("quality не может быть пустым")
	}

	config.CookiesFile = strings.TrimSpace(config.CookiesFile)
	if config.CookiesFile == "" {
		return nil
	}

	cookieData, err := os.ReadFile(config.CookiesFile)
	if err != nil {
		return fmt.Errorf("не удалось прочитать cookie-файл %s: %w", config.CookiesFile, err)
	}
	cookieHeader = strings.TrimSpace(string(cookieData))
	if cookieHeader == "" {
		return fmt.Errorf("cookie-файл %s пуст", config.CookiesFile)
	}

	return nil
}

func main() {
	if err := loadConfig(configFile); err != nil {
		fatal(err)
	}

	fmt.Println("Stepik Offline Downloader")
	fmt.Println("-------------------------")
	fmt.Printf("Курс: %d\n", config.CourseID)
	fmt.Printf("Предпочтительное качество: %sp\n", config.Quality)
	if cookieHeader != "" {
		fmt.Println("Авторизация: cookie загружены")
	} else {
		fmt.Println("Авторизация: без cookie")
	}
	fmt.Println()

	home, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}

	fmt.Println("Получаю информацию о курсе...")

	course, err := getCourse(config.CourseID)
	if err != nil {
		fatal(err)
	}

	root := filepath.Join(
		home,
		"Downloads",
		safeName(course.Title),
	)

	if err := os.MkdirAll(root, 0755); err != nil {
		fatal(err)
	}

	fmt.Printf("Курс: %s\n", course.Title)
	fmt.Printf("Секций: %d\n", len(course.Sections))
	fmt.Printf("Папка: %s\n\n", root)

	totalVideos := 0
	totalDuration := 0
	failed := 0

	for sectionIndex, sectionID := range course.Sections {
		fmt.Printf(
			"[%d/%d] Получаю секцию %d...\n",
			sectionIndex+1,
			len(course.Sections),
			sectionID,
		)

		section, err := getSection(sectionID)
		if err != nil {
			fmt.Printf("  ! Ошибка секции: %v\n\n", err)
			failed++
			continue
		}

		sectionFolder := fmt.Sprintf(
			"%s %s",
			pad(sectionIndex+1, len(course.Sections)),
			safeName(section.Title),
		)

		sectionPath := filepath.Join(root, sectionFolder)

		if err := os.MkdirAll(sectionPath, 0755); err != nil {
			fmt.Printf("  ! Не удалось создать папку: %v\n\n", err)
			failed++
			continue
		}

		fmt.Printf(
			"  %s — уроков: %d\n",
			section.Title,
			len(section.Units),
		)

		for unitIndex, unitID := range section.Units {
			unit, err := getUnit(unitID)
			if err != nil {
				fmt.Printf(
					"    [%d/%d] ! unit %d: %v\n",
					unitIndex+1,
					len(section.Units),
					unitID,
					err,
				)
				failed++
				continue
			}

			lesson, err := getLesson(unit.Lesson)
			if err != nil {
				fmt.Printf(
					"    [%d/%d] ! lesson %d: %v\n",
					unitIndex+1,
					len(section.Units),
					unit.Lesson,
					err,
				)
				failed++
				continue
			}

			fmt.Printf(
				"    [%d/%d] %s\n",
				unitIndex+1,
				len(section.Units),
				lesson.Title,
			)

			videoNumber := 0
			foundVideo := false

			for _, stepID := range lesson.Steps {
				step, err := getStep(stepID)
				if err != nil {
					fmt.Printf(
						"      ! step %d: %v\n",
						stepID,
						err,
					)
					continue
				}

				if step.Block.Name != "video" ||
					step.Block.Video == nil {
					continue
				}

				foundVideo = true
				videoNumber++

				video := *step.Block.Video

				url, actualQuality, err := chooseVideoURL(video)
				if err != nil {
					fmt.Printf("      ! %v\n", err)
					failed++
					continue
				}

				baseName := fmt.Sprintf(
					"%s %s",
					pad(unitIndex+1, len(section.Units)),
					safeName(lesson.Title),
				)

				// Если в одном уроке несколько видео —
				// добавляем номер видео.
				if videoNumber > 1 {
					baseName += fmt.Sprintf(
						" - видео %02d",
						videoNumber,
					)
				}

				target := filepath.Join(
					sectionPath,
					baseName+".mp4",
				)

				fmt.Printf(
					"      ↓ %sp, %s\n",
					actualQuality,
					formatDuration(video.Duration),
				)

				if err := downloadFile(url, target); err != nil {
					fmt.Printf("      ! Ошибка скачивания: %v\n", err)
					failed++
					continue
				}

				totalVideos++
				totalDuration += video.Duration

				fmt.Printf(
					"      ✓ %s\n",
					filepath.Base(target),
				)
			}

			if !foundVideo {
				fmt.Println("      — видео нет")
			}

			// Не долбим API слишком агрессивно.
			time.Sleep(100 * time.Millisecond)
		}

		fmt.Println()
	}

	fmt.Println("-------------------------")
	fmt.Println("Готово.")
	fmt.Printf("Видео: %d\n", totalVideos)
	fmt.Printf("Продолжительность: %s\n", formatDuration(totalDuration))
	fmt.Printf("Ошибок: %d\n", failed)
	fmt.Printf("Папка: %s\n", root)
}

func formatDuration(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60

	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}

	return fmt.Sprintf("%d:%02d", m, s)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "\nОшибка: %v\n", err)
	os.Exit(1)
}
