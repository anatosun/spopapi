package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"spopapi/internal/apiproxy"
)

const spotifyAPIBase = "https://api.spotify.com"

type trackRow struct {
	rowid, fetchedAt                     int64
	id, name                             string
	previewURL                           *string
	albumRowid, trackNumber              int64
	isrc, ean, upc                       *string
	popularity, marketsRowid, discNumber int64
	durationMS, explicit                 int64
}

type albumRow struct {
	rowid, fetchedAt                   int64
	id, name, albumType                string
	marketsRowid                       int64
	upc, amgid, copyrightC, copyrightP *string
	label                              string
	popularity                         int64
	releaseDate, releaseDatePrecision  string
	totalTracks                        int64
}

const trackColumns = `rowid, id, fetched_at, name, preview_url, album_rowid,
	track_number, external_id_isrc, external_id_ean, external_id_upc, popularity,
	available_markets_rowid, disc_number, duration_ms, explicit`

func (s *Store) GetCatalogResponse(ctx context.Context, path string, q url.Values) (apiproxy.StoredResponse, bool, error) {
	if len(q) != 0 {
		return apiproxy.StoredResponse{}, false, nil
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "v1" || parts[2] == "" {
		return apiproxy.StoredResponse{}, false, nil
	}

	var object any
	var found bool
	var err error
	switch parts[1] {
	case "tracks":
		object, found, err = s.trackObject(ctx, parts[2])
	case "albums":
		object, found, err = s.fullAlbumObject(ctx, parts[2])
	case "artists":
		object, found, err = s.artistObject(ctx, parts[2])
	case "audio-features":
		object, found, err = s.audioFeaturesObject(ctx, parts[2])
	default:
		return apiproxy.StoredResponse{}, false, nil
	}
	if err != nil || !found {
		return apiproxy.StoredResponse{}, found, err
	}
	body, err := json.Marshal(object)
	if err != nil {
		return apiproxy.StoredResponse{}, false, err
	}
	return apiproxy.StoredResponse{Status: http.StatusOK, ContentType: "application/json", Body: body}, true, nil
}

func reference(kind, id string) map[string]any {
	return map[string]any{
		"external_urls": map[string]any{"spotify": "https://open.spotify.com/" + kind + "/" + id},
		"href":          spotifyAPIBase + "/v1/" + kind + "s/" + id,
		"id":            id,
		"type":          kind,
		"uri":           "spotify:" + kind + ":" + id,
	}
}

func artistReference(id, name string) map[string]any {
	object := reference("artist", id)
	object["name"] = name
	return object
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrack(scanner rowScanner) (trackRow, error) {
	var t trackRow
	err := scanner.Scan(&t.rowid, &t.id, &t.fetchedAt, &t.name, &t.previewURL,
		&t.albumRowid, &t.trackNumber, &t.isrc, &t.ean, &t.upc, &t.popularity,
		&t.marketsRowid, &t.discNumber, &t.durationMS, &t.explicit)
	return t, err
}

func (s *Store) trackObject(ctx context.Context, id string) (map[string]any, bool, error) {
	t, err := scanTrack(s.pool.QueryRow(ctx,
		`SELECT `+trackColumns+` FROM catalog_tracks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	album, found, err := s.albumBy(ctx, "rowid", t.albumRowid)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, fmt.Errorf("track %s references missing album rowid %d", id, t.albumRowid)
	}
	albumObject, err := s.albumObject(ctx, album)
	if err != nil {
		return nil, false, err
	}
	object, err := s.simplifiedTrackObject(ctx, t)
	if err != nil {
		return nil, false, err
	}
	object["album"] = albumObject
	object["external_ids"] = externalIDs(t.isrc, t.ean, t.upc)
	object["popularity"] = t.popularity
	return object, true, nil
}

func (s *Store) simplifiedTrackObject(ctx context.Context, t trackRow) (map[string]any, error) {
	object := reference("track", t.id)
	artists, err := s.trackArtists(ctx, t.rowid)
	if err != nil {
		return nil, err
	}
	markets, err := s.markets(ctx, t.marketsRowid)
	if err != nil {
		return nil, err
	}
	object["artists"] = artists
	object["available_markets"] = markets
	object["disc_number"] = t.discNumber
	object["duration_ms"] = t.durationMS
	object["explicit"] = t.explicit != 0
	object["is_local"] = false
	object["name"] = t.name
	object["preview_url"] = t.previewURL
	object["track_number"] = t.trackNumber
	return object, nil
}

func (s *Store) albumBy(ctx context.Context, column string, value any) (albumRow, bool, error) {
	if column != "rowid" && column != "id" {
		return albumRow{}, false, fmt.Errorf("invalid album lookup column %q", column)
	}
	var a albumRow
	err := s.pool.QueryRow(ctx, `SELECT rowid, id, fetched_at, name, album_type,
		available_markets_rowid, external_id_upc, external_id_amgid, copyright_c,
		copyright_p, label, popularity, release_date, release_date_precision, total_tracks
		FROM catalog_albums WHERE `+pgx.Identifier{column}.Sanitize()+` = $1`, value).
		Scan(&a.rowid, &a.id, &a.fetchedAt, &a.name, &a.albumType, &a.marketsRowid,
			&a.upc, &a.amgid, &a.copyrightC, &a.copyrightP, &a.label, &a.popularity,
			&a.releaseDate, &a.releaseDatePrecision, &a.totalTracks)
	if errors.Is(err, pgx.ErrNoRows) {
		return albumRow{}, false, nil
	}
	return a, err == nil, err
}

func (s *Store) albumObject(ctx context.Context, a albumRow) (map[string]any, error) {
	object := reference("album", a.id)
	artists, err := s.albumArtists(ctx, a.rowid)
	if err != nil {
		return nil, err
	}
	images, err := s.images(ctx, "catalog_album_images", "album_rowid", a.rowid)
	if err != nil {
		return nil, err
	}
	markets, err := s.markets(ctx, a.marketsRowid)
	if err != nil {
		return nil, err
	}
	object["album_type"] = a.albumType
	object["artists"] = artists
	object["available_markets"] = markets
	object["images"] = images
	object["name"] = a.name
	object["release_date"] = a.releaseDate
	object["release_date_precision"] = a.releaseDatePrecision
	object["total_tracks"] = a.totalTracks
	return object, nil
}

func (s *Store) fullAlbumObject(ctx context.Context, id string) (map[string]any, bool, error) {
	a, found, err := s.albumBy(ctx, "id", id)
	if err != nil || !found {
		return nil, found, err
	}
	object, err := s.albumObject(ctx, a)
	if err != nil {
		return nil, false, err
	}
	copyrights := make([]map[string]any, 0, 2)
	if a.copyrightC != nil {
		copyrights = append(copyrights, map[string]any{"text": *a.copyrightC, "type": "C"})
	}
	if a.copyrightP != nil {
		copyrights = append(copyrights, map[string]any{"text": *a.copyrightP, "type": "P"})
	}
	tracks, err := s.albumTracksPage(ctx, a)
	if err != nil {
		return nil, false, err
	}
	object["copyrights"] = copyrights
	object["external_ids"] = externalIDs(nil, nil, a.upc)
	if a.amgid != nil {
		object["external_ids"].(map[string]any)["amgid"] = *a.amgid
	}
	object["genres"] = []string{}
	object["label"] = a.label
	object["popularity"] = a.popularity
	object["tracks"] = tracks
	return object, true, nil
}

func (s *Store) albumTracksPage(ctx context.Context, a albumRow) (map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+trackColumns+` FROM catalog_tracks
		WHERE album_rowid = $1 ORDER BY disc_number, track_number, rowid LIMIT 50`, a.rowid)
	if err != nil {
		return nil, err
	}
	var trackRows []trackRow
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		trackRows = append(trackRows, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(trackRows))
	for _, t := range trackRows {
		object, err := s.simplifiedTrackObject(ctx, t)
		if err != nil {
			return nil, err
		}
		items = append(items, object)
	}
	var next any
	if a.totalTracks > int64(len(trackRows)) && len(trackRows) == 50 {
		next = fmt.Sprintf("%s/v1/albums/%s/tracks?offset=50&limit=50", spotifyAPIBase, a.id)
	}
	return map[string]any{
		"href":     fmt.Sprintf("%s/v1/albums/%s/tracks?offset=0&limit=50", spotifyAPIBase, a.id),
		"items":    items,
		"limit":    50,
		"next":     next,
		"offset":   0,
		"previous": nil,
		"total":    a.totalTracks,
	}, nil
}

func (s *Store) artistObject(ctx context.Context, id string) (map[string]any, bool, error) {
	var rowid, followers, popularity int64
	var name string
	err := s.pool.QueryRow(ctx,
		`SELECT rowid, name, followers_total, popularity FROM catalog_artists WHERE id = $1`, id).
		Scan(&rowid, &name, &followers, &popularity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	genres, err := s.genres(ctx, rowid)
	if err != nil {
		return nil, false, err
	}
	images, err := s.images(ctx, "catalog_artist_images", "artist_rowid", rowid)
	if err != nil {
		return nil, false, err
	}
	object := reference("artist", id)
	object["followers"] = map[string]any{"href": nil, "total": followers}
	object["genres"] = genres
	object["images"] = images
	object["name"] = name
	object["popularity"] = popularity
	return object, true, nil
}

func (s *Store) audioFeaturesObject(ctx context.Context, id string) (any, bool, error) {
	var trackID string
	var nullResponse int64
	var durationMS, timeSignature, key, mode *int64
	var tempo, danceability, energy, loudness, speechiness *float64
	var acousticness, instrumentalness, liveness, valence *float64
	err := s.pool.QueryRow(ctx, `SELECT track_id, null_response, duration_ms,
		time_signature, tempo, "key", mode, danceability, energy, loudness,
		speechiness, acousticness, instrumentalness, liveness, valence
		FROM catalog_audio_features WHERE track_id = $1`, id).Scan(
		&trackID, &nullResponse, &durationMS, &timeSignature, &tempo, &key, &mode,
		&danceability, &energy, &loudness, &speechiness, &acousticness,
		&instrumentalness, &liveness, &valence)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if nullResponse != 0 {
		return nil, true, nil
	}
	return map[string]any{
		"acousticness":     acousticness,
		"analysis_url":     spotifyAPIBase + "/v1/audio-analysis/" + trackID,
		"danceability":     danceability,
		"duration_ms":      durationMS,
		"energy":           energy,
		"id":               trackID,
		"instrumentalness": instrumentalness,
		"key":              key,
		"liveness":         liveness,
		"loudness":         loudness,
		"mode":             mode,
		"speechiness":      speechiness,
		"tempo":            tempo,
		"time_signature":   timeSignature,
		"track_href":       spotifyAPIBase + "/v1/tracks/" + trackID,
		"type":             "audio_features",
		"uri":              "spotify:track:" + trackID,
		"valence":          valence,
	}, true, nil
}

func (s *Store) markets(ctx context.Context, rowid int64) ([]string, error) {
	var joined string
	if err := s.pool.QueryRow(ctx,
		`SELECT available_markets FROM catalog_available_markets WHERE rowid = $1`, rowid).Scan(&joined); err != nil {
		return nil, err
	}
	if joined == "" {
		return []string{}, nil
	}
	parts := strings.Split(joined, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts, nil
}

func (s *Store) trackArtists(ctx context.Context, trackRowid int64) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id, a.name FROM catalog_track_artists ta
		JOIN catalog_artists a ON a.rowid = ta.artist_rowid
		WHERE ta.track_rowid = $1 ORDER BY ta.rowid`, trackRowid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artists := []map[string]any{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		artists = append(artists, artistReference(id, name))
	}
	return artists, rows.Err()
}

func (s *Store) albumArtists(ctx context.Context, albumRowid int64) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id, a.name FROM catalog_artist_albums aa
		JOIN catalog_artists a ON a.rowid = aa.artist_rowid
		WHERE aa.album_rowid = $1 AND aa.index_in_album IS NOT NULL
		ORDER BY aa.index_in_album, aa.rowid`, albumRowid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artists := []map[string]any{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		artists = append(artists, artistReference(id, name))
	}
	return artists, rows.Err()
}

func (s *Store) images(ctx context.Context, table, column string, rowid int64) ([]map[string]any, error) {
	query := `SELECT width, height, url FROM ` + pgx.Identifier{table}.Sanitize() +
		` WHERE ` + pgx.Identifier{column}.Sanitize() + ` = $1 ORDER BY rowid`
	rows, err := s.pool.Query(ctx, query, rowid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	images := []map[string]any{}
	for rows.Next() {
		var width, height int64
		var imageURL string
		if err := rows.Scan(&width, &height, &imageURL); err != nil {
			return nil, err
		}
		images = append(images, map[string]any{"url": imageURL, "width": width, "height": height})
	}
	return images, rows.Err()
}

func (s *Store) genres(ctx context.Context, artistRowid int64) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT genre FROM catalog_artist_genres WHERE artist_rowid = $1 ORDER BY rowid`, artistRowid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	genres := []string{}
	for rows.Next() {
		var genre string
		if err := rows.Scan(&genre); err != nil {
			return nil, err
		}
		genres = append(genres, genre)
	}
	return genres, rows.Err()
}

func externalIDs(isrc, ean, upc *string) map[string]any {
	ids := map[string]any{}
	if isrc != nil {
		ids["isrc"] = *isrc
	}
	if ean != nil {
		ids["ean"] = *ean
	}
	if upc != nil {
		ids["upc"] = *upc
	}
	return ids
}
