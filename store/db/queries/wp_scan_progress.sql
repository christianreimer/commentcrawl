-- name: InsertWPScanProgress :exec
INSERT OR REPLACE INTO wp_scan_progress (crawl, partition_idx, partition_url, candidates_found)
VALUES (?, ?, ?, ?);

-- name: GetMaxWPScannedPartition :one
SELECT CAST(COALESCE(MAX(partition_idx), -1) AS INTEGER) AS max_idx
FROM wp_scan_progress
WHERE crawl = ?;

-- name: CountWPScannedPartitions :one
SELECT COUNT(*) AS cnt
FROM wp_scan_progress
WHERE crawl = ?;
