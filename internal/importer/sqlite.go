package importer

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

func OpenSQLite(ctx context.Context, inputPath, tempDir string) (*sql.DB, func(), error) {
	path := inputPath
	temporary := ""
	if strings.HasSuffix(inputPath, ".zst") {
		out, err := os.CreateTemp(tempDir, "spopapi-*.sqlite3")
		if err != nil {
			return nil, nil, fmt.Errorf("create temporary SQLite file: %w", err)
		}
		temporary = out.Name()
		remove := func() { _ = os.Remove(temporary) }
		in, err := os.Open(inputPath)
		if err != nil {
			out.Close()
			remove()
			return nil, nil, err
		}
		decoder, err := zstd.NewReader(in)
		if err != nil {
			in.Close()
			out.Close()
			remove()
			return nil, nil, err
		}
		_, copyErr := io.CopyBuffer(out, contextReader{ctx: ctx, r: decoder}, make([]byte, 4<<20))
		closeErr := out.Close()
		decoder.Close()
		in.Close()
		if copyErr != nil {
			remove()
			return nil, nil, fmt.Errorf("decompress dump: %w", copyErr)
		}
		if closeErr != nil {
			remove()
			return nil, nil, closeErr
		}
		path = temporary
	}

	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=query_only(1)&_pragma=cache_size(-65536)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		if temporary != "" {
			_ = os.Remove(temporary)
		}
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	cleanup := func() {
		_ = db.Close()
		if temporary != "" {
			_ = os.Remove(temporary)
		}
	}
	if err := db.PingContext(ctx); err != nil {
		cleanup()
		return nil, nil, err
	}
	return db, cleanup, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}
