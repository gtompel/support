package main

import (
	"database/sql"
	"fmt"
	"log"
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
	_ "github.com/mattn/go-sqlite3"

	"github.com/blevesearch/bleve/v2"
)

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
}

func newResultCard(question, answer string, onCopy func(string), onSave func(string, string)) *ResultCard {
	card := &ResultCard{
		question: question,
		answer:   answer,
		onCopy:   onCopy,
		onSave:   onSave,
	}
	card.ExtendBaseWidget(card)
	return card
}

func (c *ResultCard) CreateRenderer() fyne.WidgetRenderer {
	questionLabel := widget.NewLabelWithStyle(c.question, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	answerLabel := widget.NewLabelWithStyle(c.answer, fyne.TextAlignLeading, fyne.TextStyle{})
	answerLabel.Wrapping = fyne.TextWrapWord

	buttons := container.NewHBox(
		widget.NewButtonWithIcon("Копировать", theme.ContentCopyIcon(), func() {
			if c.onCopy != nil {
				c.onCopy(c.answer)
			}
		}),
		widget.NewButtonWithIcon("Сохранить", theme.FolderNewIcon(), func() {
			if c.onSave != nil {
				c.onSave(c.question, c.answer)
			}
		}),
	)

	content := container.NewVBox(
		questionLabel,
		answerLabel,
		container.NewHBox(layout.NewSpacer(), buttons),
	)

	card := widget.NewCard("", "", content)
	card.Resize(fyne.NewSize(500, 0))

	return widget.NewSimpleRenderer(card)
}

func main() {
	a := app.New()
	w := a.NewWindow("Техподдержка")

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

	// 4. Создание GUI элементы
	title := canvas.NewText("Техническая поддержка", theme.ForegroundColor())
	title.TextSize = 24
	title.Alignment = fyne.TextAlignCenter

	searchLabel := widget.NewLabelWithStyle("Введите ваш вопрос:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	input := widget.NewEntry()
	input.SetPlaceHolder("Например: Как настроить VPN?")

	// История поиска
	historyList := widget.NewList(
		func() int { return 0 },
		func() fyne.CanvasObject {
			return widget.NewLabel("История поиска")
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {},
	)
	var searchHistory []string

	// Создаем контейнер для результатов
	resultsContainer := container.NewVBox()

	// Индикатор загрузки
	progress := widget.NewProgressBarInfinite()
	progress.Hide()

	// 5. Функция поиска ответа с использованием Bleve
	findAnswer := func(question string) {
		if strings.TrimSpace(question) == "" {
			dialog.ShowInformation("Предупреждение", "Пожалуйста, введите вопрос", w)
			return
		}

		// Добавляем запрос в историю
		searchHistory = append([]string{question}, searchHistory...)
		if len(searchHistory) > 10 {
			searchHistory = searchHistory[:10]
		}
		historyList.Length = func() int { return len(searchHistory) }
		historyList.UpdateItem = func(id widget.ListItemID, item fyne.CanvasObject) {
			item.(*widget.Label).SetText(searchHistory[id])
		}
		historyList.Refresh()

		// Показываем индикатор загрузки
		progress.Show()
		resultsContainer.Objects = nil
		resultsContainer.Refresh()

		// Запускаем поиск в отдельной горутине
		go func() {
			query := bleve.NewQueryStringQuery(question)
			searchRequest := bleve.NewSearchRequest(query)
			searchRequest.Size = 5 // Показываем топ-5 результатов
			searchResult, err := index.Search(searchRequest)

			// Скрываем индикатор загрузки
			progress.Hide()

			if err != nil {
				dialog.ShowError(err, w)
				return
			}

			if len(searchResult.Hits) > 0 {
				var results []struct {
					Question string
					Answer   string
				}

				for _, hit := range searchResult.Hits {
					for _, entry := range faqEntries {
						if fmt.Sprintf("%d", entry.ID) == hit.ID {
							results = append(results, struct {
								Question string
								Answer   string
							}{
								Question: entry.Question,
								Answer:   entry.Answer,
							})
							break
						}
					}
				}

				// Очищаем контейнер результатов
				resultsContainer.Objects = nil

				// Добавляем карточки с результатами
				for _, result := range results {
					card := newResultCard(result.Question, result.Answer,
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
						})
					resultsContainer.Add(card)
				}
			} else {
				dialog.ShowInformation("Результат", "По вашему запросу ничего не найдено", w)
			}

			resultsContainer.Refresh()
		}()
	}

	// 6. Кнопка "Найти"
	button := widget.NewButtonWithIcon("Найти", theme.SearchIcon(), func() {
		findAnswer(input.Text)
	})
	button.Importance = widget.HighImportance

	// Добавляем обработку Enter в поле ввода
	input.OnSubmitted = func(s string) {
		findAnswer(s)
	}

	// 7. Компоновка GUI
	searchBox := container.NewVBox(
		searchLabel,
		input,
		container.NewHBox(layout.NewSpacer(), button),
	)

	resultsTitle := canvas.NewText("Результаты поиска", theme.ForegroundColor())
	resultsTitle.TextSize = 18
	resultsTitle.Alignment = fyne.TextAlignCenter

	// Создаем скроллируемый контейнер для результатов
	scrollContainer := container.NewScroll(resultsContainer)
	scrollContainer.SetMinSize(fyne.NewSize(500, 300))

	// Создаем вкладки
	tabs := container.NewAppTabs(
		container.NewTabItem("Поиск", container.NewVBox(
			title,
			layout.NewSpacer(),
			searchBox,
			layout.NewSpacer(),
			progress,
			resultsTitle,
			scrollContainer,
		)),
		container.NewTabItem("История", container.NewVBox(
			widget.NewLabelWithStyle("История поиска", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			container.NewScroll(historyList),
		)),
		container.NewTabItem("Избранное", container.NewVBox(
			widget.NewLabelWithStyle("Избранные ответы", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			container.NewScroll(loadFavorites(db)),
		)),
	)

	// Добавляем отступы
	paddedContent := container.NewPadded(tabs)

	w.SetContent(paddedContent)
	w.Resize(fyne.NewSize(800, 600))
	w.CenterOnScreen()

	// Добавляем поддержку горячих клавиш
	if _, ok := a.(desktop.App); ok {
		w.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyReturn, Modifier: desktop.ControlModifier}, func(shortcut fyne.Shortcut) {
			findAnswer(input.Text)
		})
	}

	w.ShowAndRun()
}

// loadFavorites загружает избранные ответы из базы данных
func loadFavorites(db *sql.DB) *fyne.Container {
	rows, err := db.Query("SELECT question, answer FROM favorites ORDER BY created_at DESC")
	if err != nil {
		return container.NewVBox(widget.NewLabel("Ошибка загрузки избранного"))
	}
	defer rows.Close()

	container := container.NewVBox()
	for rows.Next() {
		var question, answer string
		if err := rows.Scan(&question, &answer); err != nil {
			continue
		}
		card := newResultCard(question, answer,
			func(text string) {
				// Копирование в буфер обмена
			},
			func(question, answer string) {
				// Удаление из избранного
			})
		container.Add(card)
	}
	return container
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
