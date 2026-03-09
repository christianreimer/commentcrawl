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

-- name: InsertScanProgress :exec
INSERT OR REPLACE INTO disqus_scan_progress (crawl, partition_idx, partition_url, candidates_found)
VALUES (?, ?, ?, ?);

-- name: GetMaxScannedPartition :one
SELECT CAST(COALESCE(MAX(partition_idx), -1) AS INTEGER) AS max_idx
FROM disqus_scan_progress
WHERE crawl = ?;

-- name: CountScannedPartitions :one
SELECT COUNT(*) AS cnt
FROM disqus_scan_progress
WHERE crawl = ?;
