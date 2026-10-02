# Stepik Offline Downloader

Программа скачивает видео из курса Stepik в `~/Downloads/<название курса>`.

## Настройка

1. Скопируйте `config.example.json` в `config.json`.
2. Укажите ID купленного курса в `course_id`.
3. Экспортируйте cookie из браузера в файл одной строкой HTTP-заголовка:

   ```text
   sessionid=...; csrftoken=...; other_cookie=...
   ```

4. Укажите путь к этому файлу в `cookies_file`.
5. Запустите:

   ```sh
   go run stepik-download.go
   ```

Для бесплатного курса `cookies_file` можно оставить пустым. Cookie дают доступ к аккаунту — не публикуйте `cookies.txt`, не добавляйте его в Git и не передавайте другим людям.
