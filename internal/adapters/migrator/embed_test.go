package migrator_test

import (
	"io/fs"
	"reflect"
	"testing"

	"github.com/OBrunoW/catwire/migrations"
)

func TestEmbeddedMigrationsMatchVersionedPattern(t *testing.T) {
	names, err := fs.Glob(migrations.Files, "V*__*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(names) == 0 || names[0] != "V001__init.sql" {
		t.Fatalf("unexpected migrations: %v", names)
	}
	all, err := fs.Glob(migrations.Files, "*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !reflect.DeepEqual(names, filterSQL(all)) {
		t.Errorf("embedded files outside pattern: %v", all)
	}
}

func filterSQL(names []string) []string {
	var sql []string
	for _, name := range names {
		if name != "embed.go" {
			sql = append(sql, name)
		}
	}
	return sql
}
