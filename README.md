# storyconv

The project for web scraping and converting HTML stories into EPUB files.

Run:

1. Create a table *story* in the PostgreSQL database with the columns: id, title, author, filepath, url, description, updated_at (timestamp), and accesscount (int)

2. Rename *.env.example* to *.env* and adjust the database connection path.

3. Run *go run main.go*, passing the book title as a search parameter.
