CREATE TABLE IF NOT EXISTS wp_candidates (
    domain     TEXT PRIMARY KEY,
    hostname   TEXT NOT NULL DEFAULT '',
    sample_url TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS results (
    domain             TEXT PRIMARY KEY,
    wp_confirmed       INTEGER NOT NULL DEFAULT 0,
    comments_endpoint  INTEGER NOT NULL DEFAULT 0,
    comment_count_hint INTEGER NOT NULL DEFAULT 0,
    api_root           TEXT NOT NULL DEFAULT '',
    error              TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS pages (
    domain                  TEXT NOT NULL,
    post_id                 INTEGER NOT NULL,
    url                     TEXT NOT NULL DEFAULT '',
    title                   TEXT NOT NULL DEFAULT '',
    comment_count_in_sample INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (domain, post_id)
);

CREATE TABLE IF NOT EXISTS disqus_candidates (
    domain            TEXT PRIMARY KEY,
    hostname          TEXT NOT NULL DEFAULT '',
    sample_url        TEXT NOT NULL DEFAULT '',
    disqus_shortname  TEXT NOT NULL DEFAULT ''
);
