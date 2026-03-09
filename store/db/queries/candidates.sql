-- name: InsertCandidate :exec
INSERT OR IGNORE INTO wp_candidates (domain, hostname, sample_url)
VALUES (?, ?, ?);

-- name: ListCandidates :many
SELECT domain, hostname, sample_url
FROM wp_candidates
ORDER BY domain;

-- name: ListUnverifiedDomains :many
SELECT ac.domain
FROM (
    SELECT domain FROM wp_candidates
    UNION
    SELECT domain FROM disqus_candidates
) AS ac
LEFT JOIN results r ON ac.domain = r.domain
WHERE r.domain IS NULL
ORDER BY ac.domain;
