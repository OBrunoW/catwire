package migrator

import (
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestMigrationVersionStripsDescriptionAndExtension(t *testing.T) {
	cases := map[string]string{
		"V001__init.sql":         "V001",
		"dir/V002__add_cols.sql": "V002",
	}
	for name, expected := range cases {
		if got := migrationVersion(name); got != expected {
			t.Errorf("%s: got %q, expected %q", name, got, expected)
		}
	}
}

func TestMigrationPatternMatchesOnlyVersionedScripts(t *testing.T) {
	scripts := fstest.MapFS{
		"V001__init.sql": {Data: []byte("")},
		"V002__more.sql": {Data: []byte("")},
		"embed.go":       {Data: []byte("")},
		"notes.sql":      {Data: []byte("")},
	}
	matched, err := fs.Glob(scripts, migrationPattern)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(matched, []string{"V001__init.sql", "V002__more.sql"}) {
		t.Errorf("unexpected matches: %v", matched)
	}
}
