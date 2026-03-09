-- name: InsertCandidate :exec
INSERT OR IGNORE INTO wp_candidates (domain, hostname, sample_url)
VALUES (?, ?, ?);

-- name: ListCandidates :many
SELECT domain, hostname, sample_url
FROM wp_candidates
ORDER BY domain;

-- name: ListUnverifiedDomains :many
SELECT c.domain
FROM wp_candidates c
LEFT JOIN results r ON c.domain = r.domain
WHERE r.domain IS NULL
ORDER BY c.domain;
