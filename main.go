package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/blevesearch/bleve/v2"
	_ "github.com/mattn/go-sqlite3"
)

var mainTabs *container.AppTabs

// FAQEntry represents a question and its corresponding answer
type FAQEntry struct {
	ID       int
	Question string
	Answer   string
}

// ResultCard представляет карточку с результатом поиска
type ResultCard struct {
	widget.BaseWidget
	question string
	answer   string
	onCopy   func(string)
	onSave   func(string, string)
	onDelete func(string, string)
}

// OllamaRequest представляет запрос к Ollama API
type OllamaRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Context string         `json:"context,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// OllamaResponse представляет ответ от Ollama API
type OllamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// NITITheme представляет кастомную тему в стиле НИТИ
type NITITheme struct {
	fyne.Theme
}

func (t *NITITheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0, G: 102, B: 204, A: 255} // Синий цвет НИТИ
	case theme.ColorNameHover:
		return color.NRGBA{R: 0, G: 82, B: 184, A: 255} // Темно-синий при наведении
	case theme.ColorNameBackground:
		return color.NRGBA{R: 250, G: 250, B: 252, A: 255} // Светло-серый фон
	case theme.ColorNameForeground:
		return color.NRGBA{R: 51, G: 51, B: 51, A: 255} // Темно-серый текст
	case theme.ColorNameButton:
		return color.NRGBA{R: 0, G: 102, B: 204, A: 255} // Цвет кнопок
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 200, G: 200, B: 200, A: 255} // Цвет неактивных кнопок
	default:
		return t.Theme.Color(name, variant)
	}
}

func (t *NITITheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 12
	case theme.SizeNameScrollBar:
		return 8
	case theme.SizeNameScrollBarSmall:
		return 4
	case theme.SizeNameText:
		return 14
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInnerPadding:
		return 8
	default:
		return t.Theme.Size(name)
	}
}

func (t *NITITheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Bold {
		return theme.DefaultTheme().Font(style)
	}
	return theme.DefaultTheme().Font(style)
}

func newResultCard(question, answer string, onCopy func(string), onSave func(string, string), onDelete func(string, string)) *ResultCard {
	card := &ResultCard{
		question: question,
		answer:   answer,
		onCopy:   onCopy,
		onSave:   onSave,
		onDelete: onDelete,
	}
	card.ExtendBaseWidget(card)
	return card
}

func (c *ResultCard) CreateRenderer() fyne.WidgetRenderer {
	questionLabel := widget.NewLabelWithStyle(c.question, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	answerLabel := widget.NewLabelWithStyle(c.answer, fyne.TextAlignLeading, fyne.TextStyle{})
	answerLabel.Wrapping = fyne.TextWrapWord
	answerLabel.Resize(fyne.NewSize(700, 0))

	copyBtn := widget.NewButtonWithIcon("Копировать", theme.ContentCopyIcon(), func() {
		if c.onCopy != nil {
			c.onCopy(c.answer)
		}
	})
	copyBtn.Importance = widget.HighImportance

	saveBtn := widget.NewButtonWithIcon("Сохранить", theme.FolderNewIcon(), func() {
		if c.onSave != nil {
			c.onSave(c.question, c.answer)
		}
	})
	saveBtn.Importance = widget.HighImportance

	deleteBtn := widget.NewButtonWithIcon("Удалить", theme.DeleteIcon(), func() {
		if c.onDelete != nil {
			c.onDelete(c.question, c.answer)
		}
	})
	deleteBtn.Importance = widget.HighImportance

	buttons := container.NewHBox(
		copyBtn,
		saveBtn,
		deleteBtn,
	)

	content := container.NewVBox(
		questionLabel,
		answerLabel,
		container.NewHBox(layout.NewSpacer(), buttons),
	)

	card := widget.NewCard("", "", content)
	card.Resize(fyne.NewSize(800, 0))

	return widget.NewSimpleRenderer(card)
}

// generateAnswer генерирует ответ с помощью Ollama
func generateAnswer(question string, context string) (string, error) {
	req := OllamaRequest{
		Model:  "mistral", // Используем модель Mistral
		Prompt: fmt.Sprintf("Вопрос: %s\nКонтекст: %s\nОтвет:", question, context),
		Stream: false,
		Options: map[string]any{
			"temperature": 0.7,
			"top_p":       0.9,
			"num_predict": 2048, // Увеличиваем максимальную длину ответа
		},
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	resp, err := http.Post("http://172.16.10.228:11434/api/generate", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("ошибка подключения к Ollama: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var ollamaResp OllamaResponse
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return "", err
	}

	return ollamaResp.Response, nil
}

// Добавляем структуру для формы
type FAQForm struct {
	question *widget.Entry
	answer   *widget.Entry
}

// Добавляем структуру для редактирования
type EditDialog struct {
	question *widget.Entry
	answer   *widget.Entry
	id       int
}

// Функция для создания диалога редактирования
func createEditDialog(db *sql.DB, w fyne.Window, id int, question, answer string, onUpdate func()) {
	dlg := &EditDialog{
		question: widget.NewMultiLineEntry(),
		answer:   widget.NewMultiLineEntry(),
		id:       id,
	}

	dlg.question.SetText(question)
	dlg.answer.SetText(answer)

	dlg.question.SetMinRowsVisible(3)
	dlg.answer.SetMinRowsVisible(10)

	content := container.NewVBox(
		widget.NewLabelWithStyle("Вопрос:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		dlg.question,
		widget.NewLabelWithStyle("Ответ:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		dlg.answer,
	)

	updateButton := widget.NewButtonWithIcon("Сохранить", theme.DocumentSaveIcon(), func() {
		_, err := db.Exec("UPDATE faq SET question = ?, answer = ? WHERE id = ?",
			dlg.question.Text, dlg.answer.Text, dlg.id)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		onUpdate()
		w.Close()
	})
	updateButton.Importance = widget.HighImportance

	content.Add(container.NewHBox(
		layout.NewSpacer(),
		updateButton,
	))

	scroll := container.NewScroll(content)
	scroll.SetMinSize(fyne.NewSize(800, 600))

	dialog.ShowCustom("Редактирование", "Закрыть", scroll, w)
}

// Обновляем функцию createFAQForm
func createFAQForm(db *sql.DB, w fyne.Window) fyne.CanvasObject {
	form := &FAQForm{
		question: widget.NewMultiLineEntry(),
		answer:   widget.NewMultiLineEntry(),
	}

	form.question.SetPlaceHolder("Введите вопрос")
	form.answer.SetPlaceHolder("Введите ответ")

	faqListContainer := container.NewVBox()
	var updateFAQList func()
	updateFAQList = func() {
		faqListContainer.Objects = nil
		rows, err := db.Query("SELECT id, question, answer FROM faq ORDER BY id DESC")
		if err != nil {
			faqListContainer.Add(widget.NewLabel("Ошибка загрузки ответов"))
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			var question, answer string
			if err := rows.Scan(&id, &question, &answer); err != nil {
				continue
			}

			questionLabel := widget.NewLabelWithStyle(question, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			answerLabel := widget.NewLabelWithStyle(answer, fyne.TextAlignLeading, fyne.TextStyle{})
			answerLabel.Wrapping = fyne.TextWrapWord

			editBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
				createEditDialog(db, w, id, question, answer, updateFAQList)
			})
			editBtn.Importance = widget.HighImportance

			deleteBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				dialog.ShowConfirm("Подтверждение", "Удалить запись?", func(ok bool) {
					if ok {
						_, err := db.Exec("DELETE FROM faq WHERE id = ?", id)
						if err != nil {
							dialog.ShowError(err, w)
							return
						}
						updateFAQList()
					}
				}, w)
			})
			deleteBtn.Importance = widget.HighImportance

			buttons := container.NewHBox(
				layout.NewSpacer(),
				editBtn,
				deleteBtn,
			)

			content := container.NewVBox(
				questionLabel,
				answerLabel,
				buttons,
			)

			card := widget.NewCard("", "", content)
			faqListContainer.Add(card)
		}
		faqListContainer.Refresh()
	}
	updateFAQList()

	addButton := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		if form.question.Text == "" || form.answer.Text == "" {
			dialog.ShowInformation("Ошибка", "Заполните все поля", w)
			return
		}

		_, err := db.Exec("INSERT INTO faq (question, answer) VALUES (?, ?)",
			form.question.Text, form.answer.Text)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		form.question.SetText("")
		form.answer.SetText("")
		dialog.ShowInformation("Успех", "Ответ добавлен в базу", w)
		updateFAQList()
	})
	addButton.Importance = widget.HighImportance
	addButton.Resize(fyne.NewSize(40, 40))

	formContainer := container.NewVBox(
		widget.NewLabelWithStyle("Добавить новый ответ", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Вопрос:"),
		form.question,
		widget.NewLabel("Ответ:"),
		form.answer,
		container.NewHBox(layout.NewSpacer(), addButton),
	)

	scrollContainer := container.NewScroll(faqListContainer)
	scrollContainer.SetMinSize(fyne.NewSize(800, 400))

	faqContainer := container.NewVBox(
		widget.NewLabelWithStyle("Существующие ответы:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		scrollContainer,
	)

	return container.NewVBox(
		formContainer,
		faqContainer,
	)
}

// Добавляю структуру для истории
type HistoryEntry struct {
	ID       int
	Question string
	Answer   string
	Date     string
}

// Добавляю функции для работы с историей
func saveToHistory(db *sql.DB, question, answer string) error {
	_, err := db.Exec("INSERT INTO history (question, answer) VALUES (?, ?)", question, answer)
	return err
}

func loadHistory(db *sql.DB) ([]HistoryEntry, error) {
	rows, err := db.Query("SELECT id, question, answer, date FROM history ORDER BY date DESC LIMIT 10")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.ID, &entry.Question, &entry.Answer, &entry.Date); err != nil {
			return nil, err
		}
		history = append(history, entry)
	}
	return history, nil
}

func main() {
	a := app.New()
	w := a.NewWindow("Техподдержка НИТИ")

	iconBytes, err := os.ReadFile("logo.png")
	if err != nil {
		log.Printf("Ошибка загрузки иконки: %v", err)
	} else {
		icon := fyne.NewStaticResource("logo", iconBytes)
		w.SetIcon(icon)
	}

	// Устанавливаем кастомную тему
	a.Settings().SetTheme(&NITITheme{theme.DefaultTheme()})

	// Загружаем логотип
	logo := canvas.NewImageFromFile("niti_logo_140x300.jpg")
	logo.SetMinSize(fyne.NewSize(200, 70))
	logo.FillMode = canvas.ImageFillContain
	logo.Resize(fyne.NewSize(200, 70))

	// Создаем контейнер для логотипа с отступами
	logoContainer := container.NewPadded(logo)

	// 1. Подключение к базе данных SQLite3
	db, err := sql.Open("sqlite3", "faq.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Создаем таблицу для избранных ответов
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS favorites (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			question TEXT,
			answer TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatal(err)
	}

	// Создаем таблицу для истории
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			question TEXT,
			answer TEXT,
			date DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Загрузка всех вопросов и ответов из базы данных
	faqEntries, err := loadFAQEntries(db)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Создание индекса Bleve
	index, err := createBleveIndex(faqEntries)
	if err != nil {
		log.Fatal(err)
	}
	defer index.Close()

	// Загружаем историю
	history, err := loadHistory(db)
	if err != nil {
		log.Printf("Ошибка загрузки истории: %v", err)
		history = make([]HistoryEntry, 0)
	}

	// Создаем список истории
	historyList := widget.NewList(
		func() int { return len(history) },
		func() fyne.CanvasObject {
			return container.NewVBox(
				widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel(""),
			)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			box := item.(*fyne.Container)
			questionLabel := box.Objects[0].(*widget.Label)
			answerLabel := box.Objects[1].(*widget.Label)

			questionLabel.SetText(history[id].Question)
			answerLabel.SetText(history[id].Answer)
		},
	)

	// 4. Создание GUI элементы
	title := canvas.NewText("Техническая поддержка НИТИ", theme.ForegroundColor())
	title.TextSize = 24
	title.Alignment = fyne.TextAlignCenter
	title.TextStyle = fyne.TextStyle{Bold: true}

	// Создаем контейнер для заголовка с отступами
	titleContainer := container.NewPadded(title)

	// Создаем контейнер для заголовка с логотипом
	headerContainer := container.NewVBox(
		logoContainer,
		titleContainer,
	)

	searchLabel := widget.NewLabelWithStyle("Введите ваш вопрос:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	searchLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Создаем многострочное поле ввода
	input := widget.NewMultiLineEntry()
	input.SetPlaceHolder("Например: Как настроить VPN?")
	input.Wrapping = fyne.TextWrapWord
	input.Resize(fyne.NewSize(800, 100))

	// Создаем контейнер для поля ввода с отступами и тенью
	inputContainer := container.NewPadded(input)

	// Создаем контейнер для результатов
	resultsContainer := container.NewVBox()

	// Индикатор загрузки
	progress := widget.NewProgressBarInfinite()
	progress.Hide()

	// Статус подключения к Ollama
	ollamaStatus := canvas.NewText("Статус Ollama: Проверка...", theme.ForegroundColor())
	ollamaStatus.TextStyle = fyne.TextStyle{Bold: true}

	// Функция обновления статуса
	updateOllamaStatus := func(status string, color color.Color) {
		ollamaStatus.Text = "Статус Ollama: " + status
		ollamaStatus.Color = color
		ollamaStatus.Refresh()
	}

	go func() {
		resp, err := http.Get("http://172.16.10.228:11434/api/tags")
		if err != nil {
			updateOllamaStatus("Отключено", color.NRGBA{R: 255, G: 0, B: 0, A: 255})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			updateOllamaStatus("Подключено", color.NRGBA{R: 0, G: 180, B: 0, A: 255})
		} else {
			updateOllamaStatus("Ошибка", color.NRGBA{R: 255, G: 165, B: 0, A: 255})
		}
	}()

	// 5. Функция поиска ответа с использованием Bleve и Ollama
	findAnswer := func(question string) {
		if strings.TrimSpace(question) == "" {
			dialog.ShowInformation("Предупреждение", "Пожалуйста, введите вопрос", w)
			return
		}

		// Показываем индикатор загрузки
		fyne.Do(func() {
			progress.Show()
			resultsContainer.Objects = nil
			resultsContainer.Refresh()
		})

		// Запускаем поиск в отдельной горутине
		go func() {
			// Сначала ищем точное совпадение в базе
			var foundEntry FAQEntry
			var found bool
			for _, entry := range faqEntries {
				if strings.EqualFold(strings.TrimSpace(entry.Question), strings.TrimSpace(question)) {
					foundEntry = entry
					found = true
					break
				}
			}

			var answer string
			if found {
				answer = foundEntry.Answer
			} else {
				// Если точное совпадение не найдено, ищем похожие вопросы
				query := bleve.NewQueryStringQuery(question)
				searchRequest := bleve.NewSearchRequest(query)
				searchRequest.Size = 1
				searchResult, err := index.Search(searchRequest)

				if err != nil {
					fyne.Do(func() {
						progress.Hide()
						dialog.ShowError(err, w)
					})
					return
				}

				// Если нашли похожий вопрос с достаточной релевантностью
				if len(searchResult.Hits) > 0 && searchResult.Hits[0].Score > 0.3 {
					for _, entry := range faqEntries {
						if fmt.Sprintf("%d", entry.ID) == searchResult.Hits[0].ID {
							foundEntry = entry
							answer = foundEntry.Answer
							break
						}
					}
				} else {
					// Если не нашли подходящего ответа, генерируем через Ollama
					var err error
					answer, err = generateAnswer(question, "")
					if err != nil {
						fyne.Do(func() {
							progress.Hide()
							dialog.ShowError(err, w)
						})
						return
					}
				}
			}

			// Сохраняем в историю
			if err := saveToHistory(db, question, answer); err != nil {
				log.Printf("Ошибка сохранения в историю: %v", err)
			}

			// Обновляем историю
			history, err = loadHistory(db)
			if err != nil {
				log.Printf("Ошибка загрузки истории: %v", err)
			}
			fyne.Do(func() {
				historyList.Refresh()
			})

			// Создаем карточку с ответом
			card := newResultCard(question, answer,
				func(text string) {
					w.Clipboard().SetContent(text)
					dialog.ShowInformation("Успех", "Ответ скопирован в буфер обмена", w)
				},
				func(question, answer string) {
					_, err := db.Exec("INSERT INTO favorites (question, answer) VALUES (?, ?)",
						question, answer)
					if err != nil {
						dialog.ShowError(err, w)
						return
					}
					dialog.ShowInformation("Успех", "Ответ добавлен в избранное", w)
				},
				func(question, answer string) {
					_, err := db.Exec("DELETE FROM favorites WHERE question = ? AND answer = ?", question, answer)
					if err != nil {
						dialog.ShowError(err, w)
						return
					}
					mainTabs.Items[2].Content = loadFavorites(db, w)
					mainTabs.Refresh()
					dialog.ShowInformation("Успех", "Ответ удален из избранного", w)
				},
			)

			fyne.Do(func() {
				resultsContainer.Add(card)
				resultsContainer.Refresh()
				progress.Hide()
			})
		}()
	}

	// Обновляем стиль кнопок
	searchButton := widget.NewButtonWithIcon("Найти", theme.SearchIcon(), func() {
		findAnswer(input.Text)
	})
	searchButton.Importance = widget.HighImportance

	pasteButton := widget.NewButtonWithIcon("Вставить", theme.ContentPasteIcon(), func() {
		text := w.Clipboard().Content()
		if text != "" {
			input.SetText(text)
		}
	})
	pasteButton.Importance = widget.HighImportance

	// Создаем контейнер для кнопок с отступами
	buttonsContainer := container.NewHBox(
		layout.NewSpacer(),
		pasteButton,
		searchButton,
		layout.NewSpacer(),
	)

	// Добавляем горячие клавиши
	if _, ok := a.(desktop.App); ok {
		ctrlF := &desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: desktop.ControlModifier}
		w.Canvas().AddShortcut(ctrlF, func(shortcut fyne.Shortcut) {
			input.FocusGained()
		})

		ctrlV := &desktop.CustomShortcut{KeyName: fyne.KeyV, Modifier: desktop.ControlModifier}
		w.Canvas().AddShortcut(ctrlV, func(shortcut fyne.Shortcut) {
			text := w.Clipboard().Content()
			if text != "" {
				input.SetText(text)
			}
		})
	}

	// 6. Создание вкладок
	mainTabs = container.NewAppTabs(
		container.NewTabItem("Поиск", container.NewVBox(
			headerContainer,
			container.NewHBox(layout.NewSpacer(), searchLabel, layout.NewSpacer()),
			inputContainer,
			buttonsContainer,
			progress,
			ollamaStatus,
			resultsContainer,
		)),
		container.NewTabItem("История", historyList),
		container.NewTabItem("Избранное", loadFavorites(db, w)),
		container.NewTabItem("Управление БД", createFAQForm(db, w)),
	)

	// Устанавливаем стиль вкладок
	mainTabs.SetTabLocation(container.TabLocationTop)
	mainTabs.Resize(fyne.NewSize(1200, 900))

	// 8. Установка содержимого окна
	w.SetContent(container.NewVBox(mainTabs))

	// 9. Запуск приложения
	w.Resize(fyne.NewSize(1200, 900))
	w.CenterOnScreen()
	w.ShowAndRun()
}

// loadFavorites загружает избранные ответы из базы данных
func loadFavorites(db *sql.DB, w fyne.Window) fyne.CanvasObject {
	rows, err := db.Query("SELECT question, answer FROM favorites ORDER BY created_at DESC")
	if err != nil {
		return container.NewVBox(widget.NewLabel("Ошибка загрузки избранного"))
	}
	defer rows.Close()

	content := container.NewVBox()
	content.Resize(fyne.NewSize(800, 600))
	for rows.Next() {
		var question, answer string
		if err := rows.Scan(&question, &answer); err != nil {
			continue
		}
		card := newResultCard(question, answer,
			func(text string) {
				w.Clipboard().SetContent(text)
				dialog.ShowInformation("Успех", "Ответ скопирован в буфер обмена", w)
			},
			func(question, answer string) {
				// Удаление из избранного
			},
			func(question, answer string) {
				// Удаление из избранного
				_, err := db.Exec("DELETE FROM favorites WHERE question = ? AND answer = ?", question, answer)
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				// Обновляем вкладку избранного
				mainTabs.Items[2].Content = loadFavorites(db, w)
				mainTabs.Refresh()
				dialog.ShowInformation("Успех", "Ответ удален из избранного", w)
			})
		content.Add(card)
	}
	scroll := container.NewScroll(content)
	return scroll
}

// loadFAQEntries загружает все вопросы и ответы из базы данных
func loadFAQEntries(db *sql.DB) ([]FAQEntry, error) {
	rows, err := db.Query("SELECT id, question, answer FROM faq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []FAQEntry
	for rows.Next() {
		var entry FAQEntry
		if err := rows.Scan(&entry.ID, &entry.Question, &entry.Answer); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// createBleveIndex создает и заполняет индекс Bleve
func createBleveIndex(entries []FAQEntry) (bleve.Index, error) {
	mapping := bleve.NewIndexMapping()
	index, err := bleve.New("faq.bleve", mapping)
	if err != nil {
		// Если индекс уже существует, открываем его
		index, err = bleve.Open("faq.bleve")
		if err != nil {
			return nil, err
		}
		return index, nil
	}

	for _, entry := range entries {
		if err := index.Index(fmt.Sprintf("%d", entry.ID), entry); err != nil {
			return nil, err
		}
	}

	return index, nil
}
