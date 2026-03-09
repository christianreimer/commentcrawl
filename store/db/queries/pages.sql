-- name: UpsertPage :exec
INSERT OR REPLACE INTO pages
    (domain, post_id, url, title, comment_count_in_sample)
VALUES (?, ?, ?, ?, ?);

-- name: ListPages :many
SELECT domain, post_id, url, title, comment_count_in_sample
FROM pages
ORDER BY domain, comment_count_in_sample DESC;
