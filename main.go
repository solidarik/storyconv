package main

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"storyconv/config"

	"github.com/solidarik/goutils/fileutil"
	"github.com/solidarik/goutils/strutil"

	"github.com/go-shiori/go-epub"
	"github.com/gocolly/colly"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

type Story struct {
	Id          int
	Title       string
	Url         string
	Description sql.NullString
	Author      sql.NullString
	Filepath    sql.NullString
	AccessCount int
}

type FilepathWithError struct {
	Filepath *string
	Err      error
}

func convertStory(story *Story) (*string, error) {
	resultChan := make(chan FilepathWithError)

	storyFolder := "storage/" + strutil.FilterAcceptableChars(strutil.GetLastPartOfURL(story.Url), nil)
	os.RemoveAll(storyFolder)

	fileutil.CreateFolder(storyFolder)

	c := colly.NewCollector()

	c.OnHTML("#dle-content div.story-cover figure.cover img", func(e *colly.HTMLElement) {
		imgSource := e.Request.AbsoluteURL(e.Attr("src"))
		log.Debug("Cover found:", imgSource)
		webmFilePath := storyFolder + "/" + strutil.GetLastPartOfURL(imgSource) + ".webm"
		fileutil.SaveUrlToFile(imgSource, webmFilePath)
		fileutil.ConvertWebm(webmFilePath, storyFolder+"/cover.png")
	})

	c.OnHTML("div.story-text figure.image img", func(e *colly.HTMLElement) {
		imgSource := e.Request.AbsoluteURL(e.Attr("src"))
		log.Debug("One image found:", imgSource)
		fileName := strutil.GetLastPartOfURL(imgSource)
		webmFilePath := storyFolder + "/" + fileName + ".webm"
		fileutil.SaveUrlToFile(imgSource, webmFilePath)
		fileutil.ConvertWebm(webmFilePath, storyFolder+"/"+fileName+".jpg")
	})

	c.OnHTML("div.story-text div.ftext", func(e *colly.HTMLElement) {
		// Create a new EPUB
		epub, err := epub.NewEpub(story.Title)
		if err != nil {
			log.Fatalln("Error create epub:", err)
			resultChan <- FilepathWithError{Filepath: nil, Err: err}
			return
		}
		author := ""
		if story.Author.Valid {
			author = story.Author.String
		}
		epub.SetAuthor(author)

		html := "<h1>" + story.Title + "</h1>"
		epub.AddImage(storyFolder+"/cover.png", "cover.png")
		epub.SetCover("../images/cover.png", "")

		e.ForEach("*", func(_ int, el *colly.HTMLElement) {
			tagName := el.Name
			if tagName == "figure" {
				el.ForEach("img", func(_ int, imgEl *colly.HTMLElement) {
					imgSource := imgEl.Request.AbsoluteURL(imgEl.Attr("src"))
					fileName := strutil.GetLastPartOfURL(imgSource) + ".jpg"
					imgEl.DOM.RemoveAttr("loading")
					imgEl.DOM.SetAttr("src", "../images/"+fileName)
					newSrc := storyFolder + "/" + fileName
					epub.AddImage(newSrc, fileName)
				})
			}
			section_html, err := el.DOM.Html()
			if err != nil {
				log.Fatalln("Error getting HTML:", err)
				resultChan <- FilepathWithError{Filepath: nil, Err: err}
				return
			}
			html = html + section_html
			log.Debugf("Tag name: %+v, text: %+v\n", tagName, html)
		})

		epub.AddSection(html, "", "", "")
		log.Debugf("HTML: %+v\n", html)

		if author != "" {
			author = author + " - "
		}
		fileName := strutil.Transliterate(fmt.Sprintf("%+v%+v", author, story.Title), nil)
		epubFilepath := fmt.Sprintf("%+v/%+v.epub", storyFolder, fileName)
		log.Debug("Print epubFilepath: ", epubFilepath)
		err = epub.Write(epubFilepath)
		if err != nil {
			log.Fatalln("Error saving epub:", err)
			resultChan <- FilepathWithError{Filepath: nil, Err: err}
			return
		}
		resultChan <- FilepathWithError{Filepath: &epubFilepath, Err: nil}
	})

	go func() {
		defer close(resultChan)
		err := c.Visit(story.Url)
		if err != nil {
			log.Fatalln("Error visiting URL:", err)
			resultChan <- FilepathWithError{Filepath: nil, Err: err}
		}
	}()

	res := <-resultChan
	return res.Filepath, res.Err
}

func searchStory(search string) ([]Story, error) {
	var stories []Story
	db, err := connectDB()
	if err != nil {
		log.Fatalln("Error connecting to the database:", err)
		return nil, err
	}
	defer db.Close()

	query := `SELECT id, title, url, description, author, filepath, accesscount
        FROM story WHERE title ILIKE $1`

	var author string

	re := regexp.MustCompile(`(?i)автор`)
	if re.MatchString(search) {
		parts := re.Split(search, 2)
		search = strutil.TrimByChars(parts[0])
		author = strutil.TrimByChars(parts[1])
	}

	if author != "" {
		query = query + ` and author ILIKE $2`
	}

	rows, err := db.Query(query, fmt.Sprintf("%%%s%%", search))
	if author != "" {
		rows, err = db.Query(query, fmt.Sprintf("%%%s%%", search), fmt.Sprintf("%%%s%%", author))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var story Story
		err := rows.Scan(&story.Id, &story.Title, &story.Url, &story.Description,
			&story.Author, &story.Filepath, &story.AccessCount)
		if err != nil {
			return nil, err
		}
		stories = append(stories, story)
	}

	return stories, nil
}

func updateStory(story Story, filepath string) {
	db, err := connectDB()
	if err != nil {
		log.Fatalln("Error connecting to the database:", err)
		return
	}
	defer db.Close()

	query := `UPDATE story SET filepath = $1, accesscount = $2 WHERE id = $3`
	_, err = db.Exec(query, filepath, story.AccessCount+1, story.Id)
	if err != nil {
		log.Fatalln("Error updating story filepath:", err)
	}
}

func updateStoryAccessCount(story Story) {
	db, err := connectDB()
	if err != nil {
		log.Fatalln("Error connecting to the database:", err)
		return
	}
	defer db.Close()

	query := `UPDATE story SET accesscount = $1 WHERE id = $2`
	_, err = db.Exec(query, story.AccessCount+1, story.Id)
	if err != nil {
		log.Fatalln("Error updating story filepath:", err)
	}
}

func connectDB() (*sql.DB, error) {
	config := config.GetConfig()
	db, err := sql.Open("postgres", config.DbPath)
	return db, err
}

func main() {
	log.SetLevel(logrus.InfoLevel)

	stories, err := searchStory("Стрекоза и муравей автор Крылов")
	if err != nil {
		log.Fatalln("Error searching for story:", err)
		return
	}

	if stories != nil {
		if len(stories) == 1 {
			story := stories[0]
			log.Printf("Название: %s\n", story.Title)
			log.Printf("Автор: %s\n", story.Author.String)
			log.Printf("Адрес: %s\n", story.Url)

			storyFilepath := ""
			if !story.Filepath.Valid {
				log.Debugln("Starting convert story...")
				storyFilepathPtr, err := convertStory(&story)
				storyFilepath = *storyFilepathPtr
				if err != nil {
					updateStory(story, storyFilepath)
				}
				log.Debugf("The story converted. Filepath: %s\n", storyFilepath)
			} else {
				storyFilepath = story.Filepath.String
				updateStoryAccessCount(story)
				log.Debugf("Already converted story. Filepath: %s\n", storyFilepath)
			}
			log.Printf("Файл: %s", storyFilepath)
		} else {
			story := stories[len(stories)-1]
			log.Printf("Найдено несколько рассказов, уточните автора в формате \"<название> автор <автор>\".\n")
			log.Printf("Например попробуйте в таком формате: \"%s автор %s\":\n", story.Title, story.Author.String)
			log.Printf("Список найденных рассказов (%d шт):\":\n", len(stories))
			for _, story := range stories {
				log.Printf("Название: %s, автор: %s\n", story.Title, story.Author.String)
			}
		}
	} else {
		log.Println("Не найдено ни одной книги")
	}
}
