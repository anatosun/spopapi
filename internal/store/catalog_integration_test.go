package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/klauspost/compress/zstd"

	"spopapi/internal/importer"
	"spopapi/internal/store"
)

const sqliteSchema = `
CREATE TABLE available_markets (rowid integer PRIMARY KEY, available_markets text NOT NULL);
CREATE TABLE artists (rowid integer PRIMARY KEY, id text NOT NULL, fetched_at integer NOT NULL, name text NOT NULL, followers_total integer NOT NULL, popularity integer NOT NULL);
CREATE TABLE artist_genres (artist_rowid integer NOT NULL, genre text NOT NULL);
CREATE TABLE artist_images (artist_rowid integer NOT NULL, width integer NOT NULL, height integer NOT NULL, url text NOT NULL);
CREATE TABLE artist_albums (artist_rowid integer NOT NULL, album_rowid integer NOT NULL, is_appears_on integer NOT NULL, is_implicit_appears_on integer NOT NULL, index_in_album integer);
CREATE TABLE albums (rowid integer PRIMARY KEY, id text NOT NULL, fetched_at integer NOT NULL, name text NOT NULL, album_type text NOT NULL, available_markets_rowid integer NOT NULL, external_id_upc text, external_id_amgid text, copyright_c text, copyright_p text, label text NOT NULL, popularity integer NOT NULL, release_date text NOT NULL, release_date_precision text NOT NULL, total_tracks integer NOT NULL);
CREATE TABLE album_images (album_rowid integer NOT NULL, width integer NOT NULL, height integer NOT NULL, url text NOT NULL);
CREATE TABLE tracks (rowid integer PRIMARY KEY, id text NOT NULL, fetched_at integer NOT NULL, name text NOT NULL, preview_url text, album_rowid integer NOT NULL, track_number integer NOT NULL, external_id_isrc text, external_id_ean text, external_id_upc text, popularity integer NOT NULL, available_markets_rowid integer NOT NULL, disc_number integer NOT NULL, duration_ms integer NOT NULL, explicit integer NOT NULL);
CREATE TABLE track_artists (track_rowid integer NOT NULL, artist_rowid integer NOT NULL);
CREATE TABLE track_audio_features (rowid integer PRIMARY KEY, track_id text NOT NULL, fetched_at integer NOT NULL, null_response integer NOT NULL, duration_ms integer, time_signature integer, tempo integer, key integer, mode integer, danceability real, energy real, loudness real, speechiness real, acousticness real, instrumentalness real, liveness real, valence real);
`

func TestCompressedSQLiteImportAndCatalogAssembly(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	schema := fmt.Sprintf("spopapi_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)

	testURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := testURL.Query()
	query.Set("search_path", schema)
	testURL.RawQuery = query.Encode()
	st, err := store.New(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sqlitePath := makeSyntheticDump(t)
	compressedPath := sqlitePath + ".zst"
	compressFile(t, sqlitePath, compressedPath)
	db, cleanup, err := importer.OpenSQLite(ctx, compressedPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	stats, err := importer.Import(ctx, db, st.Pool(), importer.Options{Kind: "core", BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRows != 9 || len(stats.Tables) != 9 {
		t.Fatalf("core import stats = %+v", stats)
	}
	stats, err = importer.Import(ctx, db, st.Pool(), importer.Options{Kind: "audio-features", BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRows != 2 {
		t.Fatalf("audio import rows = %d, want 2", stats.TotalRows)
	}
	resumed, err := importer.Import(ctx, db, st.Pool(), importer.Options{Kind: "core"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.TotalRows != 0 || len(resumed.Skipped) != 9 {
		t.Fatalf("resume stats = %+v", resumed)
	}

	track := getObject(t, ctx, st, "/v1/tracks/track1")
	if track["id"] != "track1" || track["name"] != "Test Song" || track["explicit"] != true {
		t.Fatalf("bad track: %+v", track)
	}
	if track["album"].(map[string]any)["id"] != "album1" {
		t.Fatalf("bad track album: %+v", track["album"])
	}
	if track["artists"].([]any)[0].(map[string]any)["id"] != "artist1" {
		t.Fatalf("bad track artists: %+v", track["artists"])
	}
	if track["external_ids"].(map[string]any)["isrc"] != "ISRC1" {
		t.Fatalf("bad external IDs: %+v", track["external_ids"])
	}

	album := getObject(t, ctx, st, "/v1/albums/album1")
	if album["label"] != "Test Label" || album["total_tracks"] != float64(1) {
		t.Fatalf("bad album: %+v", album)
	}
	items := album["tracks"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "track1" {
		t.Fatalf("bad album tracks: %+v", album["tracks"])
	}

	artist := getObject(t, ctx, st, "/v1/artists/artist1")
	if artist["followers"].(map[string]any)["total"] != float64(1234) {
		t.Fatalf("bad artist followers: %+v", artist)
	}
	if artist["genres"].([]any)[0] != "rock" {
		t.Fatalf("bad artist genres: %+v", artist)
	}

	features := getObject(t, ctx, st, "/v1/audio-features/track1")
	if features["tempo"] != float64(123) || features["type"] != "audio_features" {
		t.Fatalf("bad audio features: %+v", features)
	}
	sr, found, err := st.GetCatalogResponse(ctx, "/v1/audio-features/null1", url.Values{})
	if err != nil || !found || string(sr.Body) != "null" {
		t.Fatalf("null audio features: found=%v body=%s err=%v", found, sr.Body, err)
	}
	_, found, err = st.GetCatalogResponse(ctx, "/v1/tracks/missing", url.Values{})
	if err != nil || found {
		t.Fatalf("missing track: found=%v err=%v", found, err)
	}
	_, found, err = st.GetCatalogResponse(ctx, "/v1/tracks/track1", url.Values{"market": {"US"}})
	if err != nil || found {
		t.Fatalf("market query should use upstream: found=%v err=%v", found, err)
	}
}

func getObject(t *testing.T, ctx context.Context, st *store.Store, path string) map[string]any {
	t.Helper()
	sr, found, err := st.GetCatalogResponse(ctx, path, url.Values{})
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", path, found, err)
	}
	var object map[string]any
	if err := json.Unmarshal(sr.Body, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func makeSyntheticDump(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(sqliteSchema); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO available_markets VALUES (1, 'US,DE')`,
		`INSERT INTO artists VALUES (1, 'artist1', 1, 'Test Artist', 1234, 77)`,
		`INSERT INTO artist_genres VALUES (1, 'rock')`,
		`INSERT INTO artist_images VALUES (1, 640, 640, 'https://images/artist.jpg')`,
		`INSERT INTO albums VALUES (1, 'album1', 1, 'Test Album', 'album', 1, 'UPC1', NULL, 'Copyright C', 'Copyright P', 'Test Label', 66, '2024-01-02', 'day', 1)`,
		`INSERT INTO album_images VALUES (1, 640, 640, 'https://images/album.jpg')`,
		`INSERT INTO artist_albums VALUES (1, 1, 0, 0, 0)`,
		`INSERT INTO tracks VALUES (1, 'track1', 1, 'Test Song', 'https://preview/track.mp3', 1, 1, 'ISRC1', NULL, NULL, 55, 1, 1, 200000, 1)`,
		`INSERT INTO track_artists VALUES (1, 1)`,
		`INSERT INTO track_audio_features VALUES (1, 'track1', 1, 0, 200000, 4, 123, 9, 1, 0.5, 0.6, -7.0, 0.1, 0.2, 0.3, 0.4, 0.7)`,
		`INSERT INTO track_audio_features VALUES (2, 'null1', 1, 1, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func compressFile(t *testing.T, input, output string) {
	t.Helper()
	in, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := zstd.NewWriter(out)
	if err != nil {
		out.Close()
		t.Fatal(err)
	}
	if _, err := io.Copy(writer, in); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
