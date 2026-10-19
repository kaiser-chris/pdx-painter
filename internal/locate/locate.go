// Package locate works out what an .asset file belongs to, and opens the
// folders its entities are loaded from.
//
// It follows pdx-model-viewer: the folder above the gfx folder a file is in is
// the root of its game, DLC or mod, a DLC is read with its game, and a mod with
// the game it is for. A file in no gfx folder is a loose file, read with the
// files around it.
package locate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kaiser-chris/pdx-parser-go/asset"
	"github.com/kaiser-chris/pdx-parser-go/database"
	"github.com/kaiser-chris/pdx-parser-go/folders"
	"github.com/kaiser-chris/pdx-parser-go/report"

	"github.com/kaiser-chris/pdx-asset-go/entity"
	"github.com/kaiser-chris/pdx-asset-go/texture"
)

const (
	gfxFolder  = "gfx"
	gameFolder = "game"
	dlcFolder  = "dlc"
)

// Kind is what the root of a file is.
type Kind string

const (
	Game  Kind = "game"
	Mod   Kind = "mod"
	Loose Kind = "loose"
)

// Location is where an asset file is.
type Location struct {
	// File is the asset file, as an absolute path, and Relative its path
	// below Root with forward slashes.
	File, Relative string

	// Root is the folder the file's game or mod is read from: the game
	// folder of an installation, the root of a mod, or for a loose file the
	// file's own folder.
	Root string

	Kind Kind

	// Product is the game the file is of, by its key, such as victoria3 or
	// eu5: from the installation a game folder is in, or what a mod says it
	// is for. It is empty where neither says, and for a loose file.
	Product string
}

// Locate works out the root of an asset file.
func Locate(file string) (Location, error) {
	absolute, err := filepath.Abs(file)
	if err != nil {
		return Location{}, err
	}

	if info, err := os.Stat(absolute); err != nil {
		return Location{}, err
	} else if info.IsDir() {
		return Location{}, fmt.Errorf("%s is a folder, not an .asset file", absolute)
	}

	gfx, ok := enclosingGfx(absolute)
	if !ok {
		return Location{File: absolute, Relative: filepath.Base(absolute), Root: filepath.Dir(absolute), Kind: Loose}, nil
	}

	// Europa Universalis 5 keeps its files in layers, each with a gfx folder
	// of its own, whose parent is the root.
	own := filepath.Dir(gfx)
	if isLayer(own) {
		own = filepath.Dir(own)
	}

	relative, err := filepath.Rel(own, absolute)
	if err != nil {
		return Location{}, err
	}

	root := own
	if parent := filepath.Dir(own); strings.EqualFold(filepath.Base(parent), dlcFolder) {
		root = filepath.Dir(parent)
	}

	location := Location{File: absolute, Relative: filepath.ToSlash(relative), Root: root, Kind: Game}
	if IsMod(root) {
		location.Kind = Mod
		location.Product = modProduct(root)
	} else {
		location.Product = gameProduct(root)
	}

	return location, nil
}

// knownLayers are the layers Europa Universalis 5 splits its files into.
var knownLayers = []string{"in_game", "main_menu", "loading_screen"}

// isLayer reports whether a folder holding a gfx folder is a layer of a game
// or mod rather than its root: one of the layers the games are known to use,
// below a folder that holds no gfx folder of its own.
func isLayer(dir string) bool {
	parent := filepath.Dir(dir)
	if parent == dir || exists(filepath.Join(parent, gfxFolder)) {
		return false
	}

	for _, layer := range knownLayers {
		if strings.EqualFold(filepath.Base(dir), layer) {
			return true
		}
	}

	return false
}

// The games, by the keys their mods' metadata.json names them with.
const (
	Victoria3          = "victoria3"
	EuropaUniversalis5 = "eu5"
	CrusaderKings3     = "ck3"
)

// gameProduct tells the game of a game folder from the installation it is
// in, which Steam names after the game.
func gameProduct(root string) string {
	switch strings.ToLower(filepath.Base(filepath.Dir(root))) {
	case "victoria 3":
		return Victoria3
	case "europa universalis v":
		return EuropaUniversalis5
	case "crusader kings iii":
		return CrusaderKings3
	}

	return ""
}

// modProduct is the game a mod says it is for, in the game_id of its
// metadata.json, or empty where it does not say; a descriptor.mod alone is
// Crusader Kings 3's.
func modProduct(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".metadata", "metadata.json"))
	if err == nil {
		var parsed struct {
			GameID string `json:"game_id"`
		}

		if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &parsed) == nil {
			switch strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(parsed.GameID)) {
			case "victoria3", "vic3":
				return Victoria3
			case "eu5", "europauniversalis5", "europauniversalisv":
				return EuropaUniversalis5
			case "ck3", "crusaderkings3", "crusaderkingsiii":
				return CrusaderKings3
			}
		}
	}

	if !exists(filepath.Join(root, ".metadata")) && exists(filepath.Join(root, "descriptor.mod")) {
		return CrusaderKings3
	}

	return ""
}

// IsMod reports whether a folder is the root of a mod, which describes
// itself by a .metadata folder or a descriptor.mod.
func IsMod(root string) bool {
	return exists(filepath.Join(root, ".metadata")) || exists(filepath.Join(root, "descriptor.mod"))
}

// IsGame reports whether a folder looks like the game folder of an
// installation: one named game, with a gfx folder.
func IsGame(root string) bool {
	return strings.EqualFold(filepath.Base(root), gameFolder) && exists(filepath.Join(root, gfxFolder))
}

func enclosingGfx(file string) (string, bool) {
	for dir := filepath.Dir(file); ; {
		if strings.EqualFold(filepath.Base(dir), gfxFolder) {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// Opened is the folders an asset file's entities are loaded from.
type Opened struct {
	Location Location
	Loader   *entity.Loader

	// Defined are the entities of the asset file itself.
	Defined []string

	// Diagnostics are problems with the folders, such as a mod read without
	// its game.
	Diagnostics report.Diagnostics
}

// Open reads the definitions an asset file's entities are loaded with: those
// of its game, or of its mod over the game it is for, which game says where
// it is. A loose file is read on its own.
func Open(location Location, game string) (*Opened, error) {
	defined, syntax, err := List(location)
	if err != nil {
		return nil, err
	}

	opened := &Opened{Location: location, Defined: defined, Diagnostics: syntax}

	if location.Kind == Loose {
		file := folders.File{Relative: location.Relative, Path: location.File, Source: filepath.Base(location.Root)}
		parsed := database.ParseFiles([]folders.File{file}, &report.Collector{})
		assets := asset.Read(parsed[0].Document, file, &report.Collector{})

		loader := entity.NewLoader(entity.Folder{Path: location.Root, Name: filepath.Base(location.Root)}, assets)
		loader.MissingTexture = texture.Checkerboard()
		loader.EmptyWithoutMesh = true
		loader.ByName = true
		opened.Loader = loader

		return opened, nil
	}

	engine := location.Root
	var sources []folders.Source

	if location.Kind == Mod {
		if game == "" {
			opened.Diagnostics = append(opened.Diagnostics, report.Diagnostic{
				Severity: report.SeverityWarning, Source: filepath.Base(location.Root),
				Message: "the mod is read without its game, since no game folder is set; what it takes from the game is missing",
			})
			engine = ""
		} else {
			engine = game
		}
	}

	if engine != "" {
		sources = append(entity.EngineFolders(engine), folders.Source{Name: "game", Path: engine})
	}

	if location.Kind == Mod {
		sources = append(sources, folders.Source{Name: "mod", Path: location.Root})
	}

	set := folders.Open(sources)
	opened.Diagnostics = append(opened.Diagnostics, set.Diagnostics...)
	opened.Loader = entity.NewLoader(set, asset.Load(set))

	return opened, nil
}

// List reads the entities an asset file defines, and nothing else.
func List(location Location) ([]string, report.Diagnostics, error) {
	file := folders.File{Relative: location.Relative, Path: location.File, Source: filepath.Base(location.Root)}

	syntax := &report.Collector{}
	parsed := database.ParseFiles([]folders.File{file}, syntax)
	if len(parsed) == 0 || parsed[0].Document == nil {
		return nil, syntax.Diagnostics, fmt.Errorf("%s could not be read", location.File)
	}

	defined := asset.Read(parsed[0].Document, file, &report.Collector{})

	return defined.Entities.Keys(), syntax.Diagnostics, nil
}
