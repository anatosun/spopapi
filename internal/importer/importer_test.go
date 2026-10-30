package importer

import (
	"path/filepath"
	"testing"
)

func TestTableSpecs(t *testing.T) {
	core := CoreTables()
	if len(core) != 9 {
		t.Fatalf("core table count = %d, want 9", len(core))
	}
	for _, spec := range append(core, AudioFeatureTables()...) {
		if spec.Name == "" || spec.Query == "" || len(spec.Columns) == 0 {
			t.Fatalf("incomplete table spec: %+v", spec)
		}
	}
	if _, err := Tables("playlists"); err == nil {
		t.Fatal("expected an error for unsupported dump kind")
	}
}

func TestDumpPath(t *testing.T) {
	ingest := filepath.Join("data", "ingest")
	tests := []struct {
		kind, explicit, want string
	}{
		{kind: "core", want: filepath.Join(ingest, "spotify_clean.sqlite3.zst")},
		{kind: "audio-features", want: filepath.Join(ingest, "spotify_clean_audio_features.sqlite3.zst")},
		{kind: "core", explicit: "other.sqlite3", want: "other.sqlite3"},
	}
	for _, test := range tests {
		got, err := DumpPath(test.kind, test.explicit, ingest)
		if err != nil || got != test.want {
			t.Errorf("DumpPath(%q, %q): got %q, %v; want %q", test.kind, test.explicit, got, err, test.want)
		}
	}
	if _, err := DumpPath("core", "", ""); err == nil {
		t.Fatal("expected an error without INGEST_DIR or -dump")
	}
	if _, err := DumpPath("invalid", "", ingest); err == nil {
		t.Fatal("expected an error for an invalid kind")
	}
}
