// Command pdx-painter prepares the entities of the Paradox games' .asset files,
// and .mesh files on their own, for Substance 3D Painter, and packs what
// Painter exports back into the games' textures. The Painter plugin runs it;
// it can be run on its own too.
//
//	pdx-painter list <file.asset>
//	pdx-painter formats
//	pdx-painter prepare -asset <file.asset> -entity <name> -out <folder> [-game <folder>] [-attachments] [-flip-green]
//	pdx-painter prepare -mesh <file.mesh> -out <folder> [-flip-green]
//	pdx-painter pack -manifest <manifest.json> -exported <folder> (-out <folder> | -in-place | -plan <plan.json>) [-no-backup] [-dry-run]
//
// Every command writes what it did as one JSON object to standard output, or
// {"error": "..."} and exits with 1.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/kaiser-chris/pdx-painter/internal/locate"
	"github.com/kaiser-chris/pdx-painter/internal/painter"
)

// version is set when the tool is built for a release.
var version = "dev"

func main() {
	result, err := run(os.Args[1:])
	if err != nil {
		result = map[string]string{"error": err.Error()}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(result)

	if err != nil {
		os.Exit(1)
	}
}

const usage = "usage: pdx-painter list|prepare|pack|formats|version, see -h of each"

func run(args []string) (any, error) {
	if len(args) == 0 {
		return nil, errors.New(usage)
	}

	switch args[0] {
	case "formats":
		return map[string]any{"formats": painter.WritableFormats()}, nil
	case "version":
		return map[string]any{"version": version, "manifest": painter.Version}, nil
	case "list":
		return list(args[1:])
	case "prepare":
		return prepare(args[1:])
	case "pack":
		return pack(args[1:])
	}

	return nil, errors.New(usage)
}

func list(args []string) (any, error) {
	if len(args) != 1 {
		return nil, errors.New("usage: pdx-painter list <file.asset>")
	}

	location, err := locate.Locate(args[0])
	if err != nil {
		return nil, err
	}

	entities, diagnostics, err := locate.List(location)
	if err != nil {
		return nil, err
	}

	slices.Sort(entities)

	notes := []string{}
	for _, diagnostic := range diagnostics {
		notes = append(notes, diagnostic.String())
	}

	return map[string]any{
		"asset":       location.File,
		"root":        location.Root,
		"kind":        location.Kind,
		"product":     location.Product,
		"entities":    entities,
		"diagnostics": notes,
	}, nil
}

func prepare(args []string) (any, error) {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	asset := flags.String("asset", "", "the .asset file")
	entity := flags.String("entity", "", "the entity of it to prepare")
	meshFile := flags.String("mesh", "", "a .mesh file to prepare on its own, in place of -asset and -entity")
	out := flags.String("out", "", "the folder to write the mesh, channels and manifest to")
	game := flags.String("game", "", "the game folder a mod is read with, such as .../Victoria 3/game")
	attachments := flags.Bool("attachments", false, "take the entities attached to it as well")
	flipGreen := flags.Bool("flip-green", false, "turn the y of the normal maps upside down")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}

	if *meshFile != "" && *out != "" {
		return painter.PrepareMesh(*meshFile, *out, painter.Options{FlipGreen: *flipGreen})
	}

	if *asset == "" || *entity == "" || *out == "" {
		return nil, errors.New("prepare needs -asset, -entity and -out, or -mesh and -out")
	}

	location, err := locate.Locate(*asset)
	if err != nil {
		return nil, err
	}

	opened, err := locate.Open(location, *game)
	if err != nil {
		return nil, err
	}

	manifest, err := painter.Prepare(opened, *entity, *out, painter.Options{Attachments: *attachments, FlipGreen: *flipGreen})
	if err != nil {
		return nil, err
	}

	if location.Kind == locate.Mod {
		manifest.Game = *game
	}

	return manifest, nil
}

func pack(args []string) (any, error) {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "the manifest prepare wrote")
	exported := flags.String("exported", "", "the folder Painter exported the channels to")
	out := flags.String("out", "", "the folder to write the textures straight into")
	noBackup := flags.Bool("no-backup", false, "do not keep a .bak of the files written over")
	dryRun := flags.Bool("dry-run", false, "say what would be written without writing it")
	inPlace := flags.Bool("in-place", false, "write every map over the file it was read from, in place of -out")
	planPath := flags.String("plan", "", "a JSON plan of what to write where, in place of -out and -in-place")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}

	if *manifestPath == "" || *exported == "" || *out == "" && !*inPlace && *planPath == "" {
		return nil, errors.New("pack needs -manifest, -exported, and -out, -in-place or -plan")
	}

	var manifest painter.Manifest
	if err := readJSON(*manifestPath, &manifest); err != nil {
		return nil, err
	}

	options := painter.PackOptions{Exported: *exported, Out: *out, Backup: !*noBackup, DryRun: *dryRun, InPlace: *inPlace}

	if *planPath != "" {
		options.Plan = &painter.Plan{}
		if err := readJSON(*planPath, options.Plan); err != nil {
			return nil, err
		}
	}

	return painter.Pack(&manifest, options)
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return nil
}
