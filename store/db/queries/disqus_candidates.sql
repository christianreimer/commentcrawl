-- name: InsertDisqusCandidate :exec
INSERT OR IGNORE INTO disqus_candidates (domain, hostname, sample_url, disqus_shortname)
VALUES (?, ?, ?, ?);

-- name: ListDisqusCandidates :many
SELECT domain, hostname, sample_url, disqus_shortname
FROM disqus_candidates
ORDER BY domain;

-- name: ListUnverifiedDisqusDomains :many
SELECT dc.domain
FROM disqus_candidates dc
LEFT JOIN results r ON dc.domain = r.domain
WHERE r.domain IS NULL
ORDER BY dc.domain;
