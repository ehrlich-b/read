package relay

import "database/sql"

type PipelineArticle struct {
	ID             int
	Link           string
	Title          string
	Source         string
	RawText        string
	PublishedAt    string
	Status         string
	SkipReason     string
	CompressedText string
	Mass           int
}

func (s *RelayStore) InsertPipelineArticle(link, title, source, rawText, publishedAt string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO pipeline_articles (link, title, source, raw_text, published_at) VALUES (?, ?, ?, ?, ?)`,
		link, title, source, rawText, publishedAt,
	)
	return err
}

func (s *RelayStore) ListPendingArticles(limit int) ([]PipelineArticle, error) {
	q := `SELECT id, link, title, source, raw_text, COALESCE(published_at, '') FROM pipeline_articles WHERE status = 'fetched' ORDER BY id ASC`
	if limit > 0 {
		q += " LIMIT ?"
	}
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(q, limit)
	} else {
		rows, err = s.db.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PipelineArticle
	for rows.Next() {
		var a PipelineArticle
		if err := rows.Scan(&a.ID, &a.Link, &a.Title, &a.Source, &a.RawText, &a.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *RelayStore) UpdateArticleStatus(id int, status, skipReason, compressedText string, mass int) error {
	_, err := s.db.Exec(
		`UPDATE pipeline_articles SET status = ?, skip_reason = ?, compressed_text = ?, mass = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, skipReason, compressedText, mass, id,
	)
	return err
}

func (s *RelayStore) PipelineStats() (total, fetched, compressed, posted, skipped int, err error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM pipeline_articles GROUP BY status`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err = rows.Scan(&status, &count); err != nil {
			return
		}
		total += count
		switch status {
		case "fetched":
			fetched = count
		case "compressed":
			compressed = count
		case "posted":
			posted = count
		case "skipped":
			skipped = count
		}
	}
	err = rows.Err()
	return
}

func (s *RelayStore) LinkExistsInPosts(link string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM social_embeddings WHERE link = ?`, link).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
