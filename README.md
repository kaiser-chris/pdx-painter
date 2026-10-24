# PDX Painter

A Substance 3D Painter plugin for the 3D models of Victoria 3, Europa
Universalis 5 and Crusader Kings 3. It opens an entity of an `.asset` file, or a
`.mesh` file on its own, with its textures. It then writes what you paint back
into the games' textures, in the format, size and mipmaps they were read in.

It is two halves: **`pdx-painter`**, a command line tool in Go built on
[pdx-asset-go](https://github.com/kaiser-chris/pdx-asset-go), which reads and
writes the games' files, and the **PDX Painter** Python plugin, which drives
Painter. The tool runs as its own process, so a crash in it cannot take Painter
and unsaved painting down with it.

## Installing

Download the zip for your system from the
[releases](https://github.com/kaiser-chris/pdx-painter/releases). Extract its
`PDX Painter` folder into Painter's plugin folder:

- Windows: `Documents\Adobe\Adobe Substance 3D Painter\python\plugins`
- Linux: `~/Documents/Adobe/Adobe Substance 3D Painter/python/plugins`

Then enable **PDX Painter** in Painter's **Python** menu. It needs Painter 2026
or newer.

## Using it

**PDX Painter → Open Entity or Mesh...**

- **An `.asset` file:** pick the entity to open. The game, DLC or mod the file
  belongs to is worked out from where it is. A mod is read together with its
  game, whose folder the plugin asks for once.
- **A `.mesh` file:** the textures are named after the mesh file, such as
  `house_diffuse.dds`, `house_normal.dds` and `house_properties.dds`, and not
  after the names inside the mesh. A mesh with several shapes gets a Texture
  Set for each, the ones after the first numbered (`house_2_diffuse.dds`).
  Textures of these names next to the mesh are opened. Missing ones are
  created on export, as DXT5 with full mipmaps.

Every Texture Set gets a fill layer holding the game's maps. Paint on layers
above it.

**PDX Painter → Export Textures...** asks whether to overwrite the textures
the files came from (or save next to the mesh), or to save them into another
folder as flat files. Every file written over is kept once as `.bak`. Maps that
come back unchanged are not written again.

**PDX Painter → Advanced Export...** saves what you opened as something new,
leaving the files it came from alone. Choose a folder and tick what to write:

- **Asset:** `<name>.asset`, with a `pdxmesh` `<name>_mesh` and an entity
  `<name>_entity`. The `meshsettings` keep the shaders, shader files, defines
  and subpasses of the original. Maps that aren't written are named where they
  are.
- **Mesh:** a copy of the `.mesh`, since Painter does not change geometry.
- **Diffuse, normal and properties** of each Texture Set, each in a format of
  your choosing, or kept in the one it was read in. These are ticked by
  default.

With **Derive every file name from the name** ticked, one name gives all of
them: `<name>.mesh`, `<name>_diffuse.dds` and so on, numbered for every Texture
Set after the first. Untick it to name each file yourself. It starts ticked
for a mesh, and unticked with the original names for an entity.

Only the texels the model's UVs cover are taken from Painter, because the
games share textures between models. The rest of each map stays as it was.

### Formats

DDS files are read and written with
[dds-go](https://github.com/kaiser-chris/dds-go), so any DDS format opens:
every BC compression, uncompressed layouts of any channel order, floats, and
legacy masked files. A map is written back as it was read: in the same format,
with as many mipmaps, and under the same header byte for byte, including the
exporter's flags and tags. Only the texels change. A map whose format you
change in **Advanced Export...** gets a header for its new format, DX10 where
the format needs one or the original had one.

Mipmaps are averaged channel by channel, alpha too. The games keep data in
alpha, such as a normal's y or the roughness, and weighting colour by it
would change the data. sRGB formats are averaged in linear light.
`pdx-painter formats` lists every format a map can be written in.

### Channels

| Map | Channel | Painter |
|---|---|---|
| diffuse | RGB / A | Base Color / User0 "Diffuse alpha" |
| normal | G, A (x, y) / R, B | Normal / User3, User4 where used |
| properties | R / G / B / A | User1 "Subsurface" / User2 "Specular strength" / Metallic / Roughness |

If the bumps look inverted, tick **Flip the green of normal maps** in
**PDX Painter → Settings...** and open the file again.

## The command line tool

```sh
go install github.com/kaiser-chris/pdx-painter/cmd/pdx-painter@latest
```

```sh
pdx-painter list file.asset
pdx-painter formats
pdx-painter prepare -asset file.asset -entity some_entity -out work
pdx-painter prepare -mesh file.mesh -out work
pdx-painter pack -manifest work/manifest.json -exported exported (-in-place | -out folder | -plan plan.json)
```

`prepare` writes an OBJ, a PNG per channel and a manifest. `pack` lays the
PNGs Painter exported back into the maps. Every command prints one JSON
object, or `{"error": ...}` and exits with 1.

## Development

Needs Go 1.26.

```bat
build.bat     :: tests and builds the tool into "plugin\PDX Painter\bin"
install.bat   :: copies the plugin into Painter's plugin folder
```

```sh
go test ./...
python -m unittest discover -s plugin/tests
```

The Go tests use synthetic files and need no game installed. The Python tests
cover the parts of the plugin that need no Painter.

Releases are made by hand from the **Release** workflow in the Actions tab. It
tests on Linux and Windows, bundles the plugin with the tool for each, tags the
version and publishes the release.

## Not done yet

- The mesh is read, never written, though Advanced Export copies it and writes
  an `.asset` for it.
- Painter's channels are exported in eight bits, so a map of 16 bits or floats
  comes back with the precision of eight.

## License

MIT, see [LICENSE](LICENSE).
