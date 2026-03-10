package main

import (
	"context"
	"fmt"
	"os"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/christianreimer/commentcrawl/store"
	"github.com/spf13/cobra"
)

func exportCmd() *cobra.Command {
	var (
		dbPath string
		output string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export results to a Parquet file",
		Long: `Exports Disqus candidates and verified WordPress sites to a Parquet file.

The output contains one row per site with columns:
  - hostname (string)
  - disqus_shortname (string, nullable)
  - comment_count_hint (int64, nullable)

Disqus rows have disqus_shortname set; WordPress rows have comment_count_hint set.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			rows, err := db.ReadExportRows(ctx)
			if err != nil {
				return fmt.Errorf("read export data: %w", err)
			}

			if err := writeParquet(output, rows); err != nil {
				return fmt.Errorf("write parquet: %w", err)
			}

			fmt.Printf("Exported %d rows to %s\n", len(rows), output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&dbPath, "db", "d", "commentcrawl.db", "SQLite database path")
	cmd.Flags().StringVarP(&output, "output", "o", "commentcrawl.parquet", "Output Parquet file path")

	return cmd
}

func writeParquet(path string, rows []store.ExportRow) error {
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "hostname", Type: arrow.BinaryTypes.String, Nullable: false},
		{Name: "disqus_shortname", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "comment_count_hint", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	}, nil)

	alloc := memory.DefaultAllocator

	hostBuilder := array.NewStringBuilder(alloc)
	defer hostBuilder.Release()
	shortBuilder := array.NewStringBuilder(alloc)
	defer shortBuilder.Release()
	countBuilder := array.NewInt64Builder(alloc)
	defer countBuilder.Release()

	for _, r := range rows {
		hostBuilder.Append(r.Hostname)

		if r.DisqusShortname != nil {
			shortBuilder.Append(*r.DisqusShortname)
		} else {
			shortBuilder.AppendNull()
		}

		if r.CommentCountHint != nil {
			countBuilder.Append(*r.CommentCountHint)
		} else {
			countBuilder.AppendNull()
		}
	}

	hostArr := hostBuilder.NewArray()
	defer hostArr.Release()
	shortArr := shortBuilder.NewArray()
	defer shortArr.Release()
	countArr := countBuilder.NewArray()
	defer countArr.Release()

	rec := array.NewRecord(schema, []arrow.Array{hostArr, shortArr, countArr}, int64(len(rows)))
	defer rec.Release()

	tbl := array.NewTableFromRecords(schema, []arrow.Record{rec})
	defer tbl.Release()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return pqarrow.WriteTable(tbl, f, int64(len(rows)), nil, pqarrow.DefaultWriterProps())
}
