package importer

import "fmt"

type TableSpec struct {
	Name    string
	Query   string
	Columns []string
}

func table(name, sqliteName, selectList string, columns ...string) TableSpec {
	return TableSpec{
		Name:    name,
		Query:   fmt.Sprintf("SELECT %s FROM %s", selectList, sqliteName),
		Columns: columns,
	}
}

func CoreTables() []TableSpec {
	return []TableSpec{
		table("catalog_available_markets", "available_markets",
			"rowid, available_markets",
			"rowid", "available_markets"),
		table("catalog_artists", "artists",
			"rowid, id, fetched_at, name, followers_total, popularity",
			"rowid", "id", "fetched_at", "name", "followers_total", "popularity"),
		table("catalog_artist_genres", "artist_genres",
			"rowid, artist_rowid, genre",
			"rowid", "artist_rowid", "genre"),
		table("catalog_artist_images", "artist_images",
			"rowid, artist_rowid, width, height, url",
			"rowid", "artist_rowid", "width", "height", "url"),
		table("catalog_albums", "albums",
			"rowid, id, fetched_at, name, album_type, available_markets_rowid, external_id_upc, external_id_amgid, copyright_c, copyright_p, label, popularity, release_date, release_date_precision, total_tracks",
			"rowid", "id", "fetched_at", "name", "album_type", "available_markets_rowid", "external_id_upc", "external_id_amgid", "copyright_c", "copyright_p", "label", "popularity", "release_date", "release_date_precision", "total_tracks"),
		table("catalog_album_images", "album_images",
			"rowid, album_rowid, width, height, url",
			"rowid", "album_rowid", "width", "height", "url"),
		table("catalog_artist_albums", "artist_albums",
			"rowid, artist_rowid, album_rowid, is_appears_on, is_implicit_appears_on, index_in_album",
			"rowid", "artist_rowid", "album_rowid", "is_appears_on", "is_implicit_appears_on", "index_in_album"),
		table("catalog_tracks", "tracks",
			"rowid, id, fetched_at, name, preview_url, album_rowid, track_number, external_id_isrc, external_id_ean, external_id_upc, popularity, available_markets_rowid, disc_number, duration_ms, explicit",
			"rowid", "id", "fetched_at", "name", "preview_url", "album_rowid", "track_number", "external_id_isrc", "external_id_ean", "external_id_upc", "popularity", "available_markets_rowid", "disc_number", "duration_ms", "explicit"),
		table("catalog_track_artists", "track_artists",
			"rowid, track_rowid, artist_rowid",
			"rowid", "track_rowid", "artist_rowid"),
	}
}

func AudioFeatureTables() []TableSpec {
	return []TableSpec{
		table("catalog_audio_features", "track_audio_features",
			`rowid, track_id, fetched_at, null_response, duration_ms, time_signature,
			 CAST(tempo AS REAL), key, mode, danceability, energy, loudness,
			 speechiness, acousticness, instrumentalness, liveness, valence`,
			"rowid", "track_id", "fetched_at", "null_response", "duration_ms", "time_signature",
			"tempo", "key", "mode", "danceability", "energy", "loudness", "speechiness",
			"acousticness", "instrumentalness", "liveness", "valence"),
	}
}

func Tables(kind string) ([]TableSpec, error) {
	switch kind {
	case "core":
		return CoreTables(), nil
	case "audio-features":
		return AudioFeatureTables(), nil
	default:
		return nil, fmt.Errorf("unknown dump kind %q (want core or audio-features)", kind)
	}
}
