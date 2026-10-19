package locate

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("entity = { name = \"one_entity\" }"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocate(t *testing.T) {
	base := t.TempDir()

	game := filepath.Join(base, "Victoria 3", "game")
	dlc := filepath.Join(game, "dlc", "dlc001", "gfx", "models", "x.asset")
	mod := filepath.Join(base, "my_mod")
	loose := filepath.Join(base, "loose", "x.asset")

	touch(t, filepath.Join(game, "gfx", "models", "x.asset"))
	touch(t, dlc)
	touch(t, filepath.Join(mod, ".metadata", "metadata.json"))
	touch(t, filepath.Join(mod, "gfx", "models", "deep", "x.asset"))
	touch(t, loose)

	cases := []struct {
		file, root, relative string
		kind                 Kind
	}{
		{filepath.Join(game, "gfx", "models", "x.asset"), game, "gfx/models/x.asset", Game},
		// A DLC is read with its game.
		{dlc, game, "gfx/models/x.asset", Game},
		{filepath.Join(mod, "gfx", "models", "deep", "x.asset"), mod, "gfx/models/deep/x.asset", Mod},
		{loose, filepath.Dir(loose), "x.asset", Loose},
	}

	for _, c := range cases {
		location, err := Locate(c.file)
		if err != nil {
			t.Fatal(err)
		}

		if location.Root != c.root || location.Relative != c.relative || location.Kind != c.kind {
			t.Errorf("%s: %+v, want %s %s below %s", c.file, location, c.kind, c.relative, c.root)
		}
	}

	if location, _ := Locate(filepath.Join(game, "gfx", "models", "x.asset")); location.Product != Victoria3 {
		t.Errorf("the game of a Victoria 3 installation = %q", location.Product)
	}

	if !IsGame(game) || IsGame(mod) {
		t.Error("IsGame told the game and the mod apart wrongly")
	}

	location, _ := Locate(loose)
	if location.Product != "" {
		t.Errorf("a loose file is of %q", location.Product)
	}

	entities, _, err := List(location)
	if err != nil || len(entities) != 1 || entities[0] != "one_entity" {
		t.Errorf("entities = %v, %v", entities, err)
	}
}

// Europa Universalis 5 keeps its files in layers, each with a gfx folder,
// whose parent is the root; and a mod names the game it is for.
func TestLocateLayersAndTheGameOfAMod(t *testing.T) {
	base := t.TempDir()

	game := filepath.Join(base, "Europa Universalis V", "game")
	file := filepath.Join(game, "in_game", "gfx", "models", "x.asset")
	touch(t, file)

	location, err := Locate(file)
	if err != nil {
		t.Fatal(err)
	}

	if location.Root != game || location.Relative != "in_game/gfx/models/x.asset" || location.Product != EuropaUniversalis5 {
		t.Errorf("layer = %+v", location)
	}

	mod := filepath.Join(base, "eu5_mod")
	if err := os.MkdirAll(filepath.Join(mod, ".metadata"), 0o755); err != nil {
		t.Fatal(err)
	}

	// With the byte order mark editors on Windows write.
	metadata := "\xef\xbb\xbf{\"name\": \"A mod\", \"game_id\": \"eu5\"}"
	if err := os.WriteFile(filepath.Join(mod, ".metadata", "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	touch(t, filepath.Join(mod, "in_game", "gfx", "x.asset"))

	location, _ = Locate(filepath.Join(mod, "in_game", "gfx", "x.asset"))
	if location.Kind != Mod || location.Root != mod || location.Product != EuropaUniversalis5 {
		t.Errorf("mod = %+v", location)
	}
}
