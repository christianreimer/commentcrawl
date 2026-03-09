-- name: UpsertResult :exec
INSERT OR REPLACE INTO results
    (domain, wp_confirmed, comments_endpoint, comment_count_hint,
     api_root, error)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListResults :many
SELECT domain, wp_confirmed, comments_endpoint, comment_count_hint,
       api_root, error
FROM results
ORDER BY comment_count_hint DESC;

-- name: ListConfirmedResults :many
SELECT domain, wp_confirmed, comments_endpoint, comment_count_hint,
       api_root, error
FROM results
WHERE comments_endpoint = 1
ORDER BY comment_count_hint DESC;
