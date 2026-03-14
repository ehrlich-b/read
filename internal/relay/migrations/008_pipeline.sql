CREATE TABLE pipeline_articles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    link TEXT NOT NULL,
    title TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    raw_text TEXT NOT NULL DEFAULT '',
    published_at TEXT,
    status TEXT NOT NULL DEFAULT 'fetched',
    skip_reason TEXT,
    compressed_text TEXT,
    mass INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX idx_pipeline_link ON pipeline_articles(link);
CREATE INDEX idx_pipeline_status ON pipeline_articles(status);
