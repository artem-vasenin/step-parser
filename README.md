# Stepik Offline Downloader

Программа скачивает доступные вашему аккаунту видео из курса Stepik в `~/Downloads/<название курса>`.

## Настройка

1. Скопируйте `config.example.json` в `config.json`.
2. В `course_url` вставьте обычную ссылку Stepik. Поддерживаются:
   - `https://stepik.org/course/289926/syllabus`
   - `https://stepik.org/lesson/1370048/step/1?unit=1386238`
3. Для курса, требующего авторизации, сохраните cookie из своей браузерной сессии в `cookies.txt` одной строкой HTTP-заголовка. Не публикуйте этот файл и не добавляйте его в Git.
4. Укажите желаемое качество в `quality`, например `1080`.
5. Запустите:

   ```sh
   go run main.go
   ```

   или соберите бинарник:

   ```sh
   go build -o stepik-download main.go
   ./stepik-download
   ```

Для публичного курса `cookies_file` можно оставить пустым (`""`).

Если `course_url` ведёт прямо на курс, ID берётся из URL. Если это ссылка на урок с параметром `unit`, программа автоматически проходит цепочку `unit → section → course` через Stepik API.
