package importer

import (
	"fmt"
	"path/filepath"
)

func DumpPath(kind, explicit, ingestDir string) (string, error) {
	if _, err := Tables(kind); err != nil {
		return "", err
	}
	if explicit != "" {
		return explicit, nil
	}
	if ingestDir == "" {
		return "", fmt.Errorf("set INGEST_DIR or use -dump")
	}
	filenames := map[string]string{
		"core":           "spotify_clean.sqlite3.zst",
		"audio-features": "spotify_clean_audio_features.sqlite3.zst",
	}
	return filepath.Join(ingestDir, filenames[kind]), nil
}
