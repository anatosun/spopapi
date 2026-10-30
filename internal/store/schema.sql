CREATE TABLE IF NOT EXISTS responses (
    key          text PRIMARY KEY,
    status       integer NOT NULL,
    content_type text,
    body         bytea,
    fetched_at   timestamptz NOT NULL DEFAULT now(),
    source       text NOT NULL DEFAULT 'upstream'
);

CREATE TABLE IF NOT EXISTS import_state (
    table_name  text PRIMARY KEY,
    rows        bigint NOT NULL,
    completed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS catalog_available_markets (
    rowid             bigint PRIMARY KEY,
    available_markets text NOT NULL
);

CREATE TABLE IF NOT EXISTS catalog_artists (
    rowid          bigint PRIMARY KEY,
    id             text NOT NULL UNIQUE,
    fetched_at     bigint NOT NULL,
    name           text NOT NULL,
    followers_total bigint NOT NULL,
    popularity     integer NOT NULL
);

CREATE TABLE IF NOT EXISTS catalog_artist_genres (
    rowid        bigint PRIMARY KEY,
    artist_rowid bigint NOT NULL,
    genre        text NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_artist_genres_artist ON catalog_artist_genres (artist_rowid);

CREATE TABLE IF NOT EXISTS catalog_artist_images (
    rowid        bigint PRIMARY KEY,
    artist_rowid bigint NOT NULL,
    width        integer NOT NULL,
    height       integer NOT NULL,
    url          text NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_artist_images_artist ON catalog_artist_images (artist_rowid);

CREATE TABLE IF NOT EXISTS catalog_albums (
    rowid                   bigint PRIMARY KEY,
    id                      text NOT NULL UNIQUE,
    fetched_at              bigint NOT NULL,
    name                    text NOT NULL,
    album_type              text NOT NULL,
    available_markets_rowid bigint NOT NULL,
    external_id_upc         text,
    external_id_amgid       text,
    copyright_c             text,
    copyright_p             text,
    label                   text NOT NULL,
    popularity              integer NOT NULL,
    release_date            text NOT NULL,
    release_date_precision  text NOT NULL,
    total_tracks            integer NOT NULL
);

CREATE TABLE IF NOT EXISTS catalog_album_images (
    rowid       bigint PRIMARY KEY,
    album_rowid bigint NOT NULL,
    width       integer NOT NULL,
    height      integer NOT NULL,
    url         text NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_album_images_album ON catalog_album_images (album_rowid);

CREATE TABLE IF NOT EXISTS catalog_artist_albums (
    rowid                    bigint PRIMARY KEY,
    artist_rowid             bigint NOT NULL,
    album_rowid              bigint NOT NULL,
    is_appears_on            integer NOT NULL,
    is_implicit_appears_on   integer NOT NULL,
    index_in_album           integer
);
CREATE INDEX IF NOT EXISTS catalog_artist_albums_artist ON catalog_artist_albums (artist_rowid);
CREATE INDEX IF NOT EXISTS catalog_artist_albums_album ON catalog_artist_albums (album_rowid);

CREATE TABLE IF NOT EXISTS catalog_tracks (
    rowid                   bigint PRIMARY KEY,
    id                      text NOT NULL UNIQUE,
    fetched_at              bigint NOT NULL,
    name                    text NOT NULL,
    preview_url             text,
    album_rowid             bigint NOT NULL,
    track_number            integer NOT NULL,
    external_id_isrc        text,
    external_id_ean         text,
    external_id_upc         text,
    popularity              integer NOT NULL,
    available_markets_rowid bigint NOT NULL,
    disc_number             integer NOT NULL,
    duration_ms             bigint NOT NULL,
    explicit                integer NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_tracks_album ON catalog_tracks (album_rowid);
CREATE INDEX IF NOT EXISTS catalog_tracks_isrc ON catalog_tracks (external_id_isrc);

CREATE TABLE IF NOT EXISTS catalog_track_artists (
    rowid        bigint PRIMARY KEY,
    track_rowid  bigint NOT NULL,
    artist_rowid bigint NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_track_artists_track ON catalog_track_artists (track_rowid);
CREATE INDEX IF NOT EXISTS catalog_track_artists_artist ON catalog_track_artists (artist_rowid);

CREATE TABLE IF NOT EXISTS catalog_audio_features (
    rowid            bigint PRIMARY KEY,
    track_id         text NOT NULL UNIQUE,
    fetched_at       bigint NOT NULL,
    null_response    integer NOT NULL,
    duration_ms      bigint,
    time_signature   integer,
    tempo            double precision,
    "key"            integer,
    mode             integer,
    danceability     double precision,
    energy           double precision,
    loudness         double precision,
    speechiness      double precision,
    acousticness     double precision,
    instrumentalness double precision,
    liveness         double precision,
    valence          double precision
);
