"""What the plugin does that needs nothing of Painter: running pdx-painter,
and building the export configuration from a manifest.

Kept apart from the rest so that it can be tested with any Python, outside
Painter: see tests/test_core.py.
"""

import json
import math
import os
import subprocess
import sys

# The version of the plugin, which a release writes into the copy it bundles.
VERSION = "dev"

# The manifest version pdx-painter writes, which the plugin reads.
MANIFEST_VERSION = 1

# What the plugin calls itself in a project's metadata.
METADATA_CONTEXT = "pdx_painter"

# The games, by the keys pdx-painter names them with, as their mods'
# metadata.json does, with their names and the folders Steam installs them in.
VICTORIA_3 = "victoria3"
EUROPA_UNIVERSALIS_5 = "eu5"
CRUSADER_KINGS_3 = "ck3"

GAME_ORDER = [VICTORIA_3, EUROPA_UNIVERSALIS_5, CRUSADER_KINGS_3]

GAMES = {
    VICTORIA_3: "Victoria 3",
    EUROPA_UNIVERSALIS_5: "Europa Universalis 5",
    CRUSADER_KINGS_3: "Crusader Kings 3",
}

INSTALL_FOLDERS = {
    VICTORIA_3: "Victoria 3",
    EUROPA_UNIVERSALIS_5: "Europa Universalis V",
    CRUSADER_KINGS_3: "Crusader Kings III",
}

# Painter takes Texture Set sizes that are powers of two in this range.
MIN_RESOLUTION = 128
MAX_RESOLUTION = 8192


class ToolError(Exception):
    """pdx-painter failed, with what it said."""


def tool_path(override=""):
    """The pdx-painter executable: the one set in the settings, or the one
    shipped next to the plugin."""
    if override:
        return override

    name = "pdx-painter.exe" if sys.platform == "win32" else "pdx-painter"

    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "bin", name)


def run_tool(args, tool=""):
    """Run pdx-painter with arguments and return the JSON object it writes.

    Raises ToolError with its message when it fails.
    """
    executable = tool_path(tool)
    if not os.path.isfile(executable):
        raise ToolError("pdx-painter is not at {0}; build it with build.bat and install the plugin again".format(executable))

    flags = 0
    if sys.platform == "win32":
        flags = 0x08000000  # CREATE_NO_WINDOW: no console flashes up

    completed = subprocess.run(
        [executable] + list(args),
        capture_output=True,
        text=True,
        encoding="utf-8",
        creationflags=flags,
    )

    try:
        result = json.loads(completed.stdout)
    except ValueError:
        raise ToolError("pdx-painter wrote no result: {0}{1}".format(completed.stdout, completed.stderr).strip())

    if completed.returncode != 0 or "error" in result:
        raise ToolError(result.get("error", "pdx-painter failed with exit code {0}".format(completed.returncode)))

    return result


def resolution(size):
    """The power of two nearest a size, within what Painter takes."""
    if size <= 0:
        return 1024

    nearest = 2 ** int(round(math.log2(size)))

    return max(MIN_RESOLUTION, min(MAX_RESOLUTION, nearest))


def largest_resolution(manifest):
    """The Texture Set size of the largest set of a manifest, which a new
    project starts every set at."""
    largest = 0
    for texture_set in manifest["textureSets"]:
        largest = max(largest, texture_set["width"], texture_set["height"])

    return resolution(largest)


def preset_name(texture_set):
    return "pdx-painter " + texture_set["name"]


def export_config(manifest, export_path, only=None):
    """The configuration of Painter's export that writes every channel of the
    manifest the way pdx-painter's pack reads them: one PNG of eight bits per
    channel, named after the Texture Set and the channel.

    Every Texture Set gets a preset of its own, since not every set has every
    channel. With only, a collection of Texture Set names, only those are
    exported.
    """
    presets = []
    export_list = []

    for texture_set in manifest["textureSets"]:
        if only is not None and texture_set["name"] not in only:
            continue

        maps = []

        for channel in texture_set["channels"]:
            source = {"srcMapType": channel["exportType"], "srcMapName": channel["exportName"]}

            if channel["color"]:
                channels = [dict(source, destChannel=c, srcChannel=c) for c in "RGB"]
            else:
                channels = [dict(source, destChannel="L", srcChannel="L")]

            maps.append({"fileName": "$textureSet_" + channel["id"], "channels": channels})

        presets.append({"name": preset_name(texture_set), "maps": maps})
        export_list.append({"rootPath": texture_set["name"], "exportPreset": preset_name(texture_set)})

    return {
        "exportShaderParams": False,
        "exportPath": export_path,
        "exportPresets": presets,
        "exportList": export_list,
        "exportParameters": [
            {
                "parameters": {
                    "fileFormat": "png",
                    "bitDepth": "8",
                    "dithering": False,
                    # Only what the mesh covers is taken back, so what fills
                    # the rest does not matter; infinite padding keeps the
                    # edges of the islands clean in the smaller mipmaps.
                    "paddingAlgorithm": "infinite",
                }
            }
        ],
    }


def default_out_root(manifest, remembered=""):
    """The folder the dialog for another folder starts in: the mod, loose
    folder or mesh's folder the textures came from, or for a game's own file,
    the folder used last. A game's own folder is never the default: the next
    update or a check of the game files puts it back."""
    if manifest["kind"] in ("mod", "loose", "mesh"):
        return manifest["root"]

    return remembered


def overwrites_game(manifest):
    """Whether writing the textures over the files they came from changes the
    game's own installation."""
    return manifest["kind"] == "game"


def export_question(manifest):
    """What the dialog asking where to export to says: its text, and its two
    buttons, for writing in place and for choosing another folder."""
    if manifest["kind"] == "mesh":
        new = any(stored.get("new") for texture_set in manifest["textureSets"] for stored in texture_set["maps"].values())
        name = os.path.basename(manifest["asset"])

        details = "Next to the mesh, they are named after it, such as {0}_diffuse.dds".format(os.path.splitext(name)[0])
        details += ", and the maps that are not there yet are made there." if new else "."
        details += " In another folder, they go straight into it."

        return {
            "text": "Where should the textures of {0} go?".format(name),
            "details": details,
            "inPlace": "Save Next to the Mesh",
            "elsewhere": "Save into Another Folder...",
            "folderTitle": "Save the textures into",
        }

    details = "In another folder, they go straight into it, by their file names."
    if manifest["kind"] == "game":
        details = "Overwriting changes the game's own files. " + details

    return {
        "text": "Overwrite the existing textures of {0}?".format(manifest["entity"]),
        "details": details + " A .bak is kept of every file written over.",
        "inPlace": "Overwrite",
        "elsewhere": "Save into Another Folder...",
        "folderTitle": "Save the textures into",
    }


def writes_into_game(manifest, out_root):
    """Whether writing below a root changes the game's own installation."""
    if manifest["kind"] != "game":
        return False

    return os.path.normcase(os.path.abspath(out_root)) == os.path.normcase(os.path.abspath(manifest["root"]))


def for_metadata(value):
    """A value as project metadata takes it, which has no None."""
    if isinstance(value, dict):
        return {key: for_metadata(item) for key, item in value.items() if item is not None}

    if isinstance(value, list):
        return [for_metadata(item) for item in value if item is not None]

    return value


def check_manifest(manifest):
    """Raise ToolError for a manifest of a version the plugin does not read."""
    version = manifest.get("version")
    if version != MANIFEST_VERSION:
        raise ToolError("the manifest is of version {0}, the plugin reads {1}; install matching versions".format(version, MANIFEST_VERSION))


# --- Advanced export ----------------------------------------------------------

# The kinds of map a Texture Set has, in the order the dialog lists them, with
# what it calls them.
MAP_KINDS = [("diffuse", "Diffuse"), ("normal", "Normal"), ("properties", "Properties")]

# The formats a map can be written in, by the names pdx-painter takes, with
# what the dialog calls them. The empty one keeps the format it was read in.
FORMATS = [("", "Keep"), ("BC1", "DXT1"), ("BC3", "DXT5"), ("BC7", "BC7"), ("BGRA8", "Uncompressed")]

FORMAT_NAMES = {"BC1": "DXT1", "BC3": "DXT5", "BC7": "BC7", "BGRA8": "Uncompressed", "RGBA8": "Uncompressed",
                "PNG": "PNG", "TGA": "TGA"}


def keep_label(stored):
    """What the dialog calls keeping a map's format: Keep (DXT5)."""
    return "Keep ({0})".format(FORMAT_NAMES.get(stored.get("format", ""), stored.get("format", "")))


def stem(path):
    """A file's name without its folder and extension."""
    return os.path.splitext(os.path.basename(path))[0]


def default_name(manifest):
    """The name an advanced export starts with: the mesh's for a mesh, the
    entity's without its _entity for an entity."""
    name = manifest["entity"]
    if manifest["kind"] != "mesh" and name.endswith("_entity"):
        name = name[:-len("_entity")]

    return name


def default_folder(manifest):
    """The folder an advanced export starts in: the one of the file opened."""
    return os.path.dirname(manifest["asset"])


def derives_by_default(manifest):
    """Whether an advanced export starts deriving every name from one: for a
    mesh, whose names are derived already, but not for an entity, whose names
    are kept as they are."""
    return manifest["kind"] == "mesh"


def map_extension(stored, chosen=""):
    """The extension a map is written with: that of the format chosen, or kept
    from the one it was read in."""
    format_name = chosen or stored.get("format", "")

    return {"PNG": ".png", "TGA": ".tga"}.get(format_name, ".dds")


def derived_names(manifest, name, chosen=None):
    """Every file name an advanced export writes, derived from one name: the
    asset and mesh by it, and the maps of the first Texture Set by it and of the
    others by it and their number. chosen are the formats chosen for the maps,
    by (set, kind), which say their extensions.

    Returns {"asset": ..., "mesh": ..., "maps": {(set, kind): ...}}.
    """
    chosen = chosen or {}
    names = {"asset": name + ".asset", "mesh": name + ".mesh", "maps": {}}

    for number, texture_set in enumerate(manifest["textureSets"]):
        prefix = name if number == 0 else "{0}_{1}".format(name, number + 1)

        for kind, _ in MAP_KINDS:
            stored = texture_set["maps"].get(kind)
            if stored:
                key = (texture_set["name"], kind)
                names["maps"][key] = "{0}_{1}{2}".format(prefix, kind, map_extension(stored, chosen.get(key, "")))

    return names


def existing_names(manifest):
    """The file names of what was opened, which an advanced export of an entity
    starts with: the asset's, the mesh's and each map's own."""
    if manifest["kind"] == "mesh":
        names = derived_names(manifest, default_name(manifest))
    else:
        names = {"asset": os.path.basename(manifest["asset"]), "maps": {}}

    mesh_file = manifest.get("meshFile")
    names["mesh"] = os.path.basename(mesh_file["path"]) if mesh_file else default_name(manifest) + ".mesh"

    for texture_set in manifest["textureSets"]:
        for kind, _ in MAP_KINDS:
            stored = texture_set["maps"].get(kind)
            if stored:
                names["maps"][(texture_set["name"], kind)] = os.path.basename(stored["relative"])

    return names


def asset_definitions(asset_file):
    """The names of the pdxmesh and the entity an asset of a file name defines:
    house_mesh and house_entity for house.asset."""
    name = stem(asset_file)

    return name + "_mesh", name + "_entity"


def can_write_mesh(manifest):
    """Whether the mesh can be copied, and an asset written: only where the
    mesh file is known."""
    return bool(manifest.get("meshFile"))


def can_write_asset(manifest):
    """Whether an asset can be written: one needs the mesh file, and a mesh of
    it to give mesh settings."""
    return can_write_mesh(manifest) and any(texture_set.get("meshes") for texture_set in manifest["textureSets"])


def build_plan(folder, asset=None, mesh=None, maps=()):
    """The plan pdx-painter's pack carries out, from what the dialog chose.

    asset and mesh are the file names to write them as, or None; maps are
    (set, kind, file, format, extension) for each map chosen, the extension
    being what its file is to end in, which pack does not read.
    """
    plan = {"folder": folder, "maps": []}

    for texture_set, kind, file, format_name, extension in maps:
        planned = {"set": texture_set, "kind": kind, "file": file, "extension": extension}
        if format_name:
            planned["format"] = format_name

        plan["maps"].append(planned)

    if mesh:
        plan["mesh"] = {"file": mesh}

    if asset:
        mesh_name, entity_name = asset_definitions(asset)
        plan["asset"] = {"file": asset, "mesh": mesh_name, "entity": entity_name}

    return plan


def plan_problems(plan):
    """What is wrong with a plan, as the dialog says it before exporting: an
    empty or wrong file name, or one file chosen twice. Empty when nothing is."""
    problems = []

    if not plan["folder"].strip():
        problems.append("Choose a folder to export into.")

    files = []

    def check(what, file, extension):
        if not file.strip():
            problems.append("Give the {0} a file name.".format(what))
        elif os.path.basename(file) != file:
            problems.append("The {0} is to be a file name, not a path: {1}".format(what, file))
        elif not file.lower().endswith(extension):
            problems.append("The {0} {1} is to end in {2}.".format(what, file, extension))
        else:
            files.append(file.lower())

    if not plan["maps"] and "mesh" not in plan and "asset" not in plan:
        problems.append("Choose something to export.")

    for planned in plan["maps"]:
        check("{0} map of {1}".format(planned["kind"], planned["set"]), planned["file"], planned.get("extension", ".dds"))

    if "mesh" in plan:
        check("mesh", plan["mesh"]["file"], ".mesh")

    if "asset" in plan:
        check("asset", plan["asset"]["file"], ".asset")

    for file in sorted(set(name for name in files if files.count(name) > 1)):
        problems.append("{0} is chosen for two files.".format(file))

    return problems


def existing_files(plan):
    """The files a plan writes over, which the dialog warns of."""
    names = [planned["file"] for planned in plan["maps"]]
    names += [plan[key]["file"] for key in ("mesh", "asset") if key in plan]

    return [name for name in names if name and os.path.isfile(os.path.join(plan["folder"], name))]


def inside_game(manifest, folder):
    """Whether a folder is inside the game's own installation, for an entity
    of the game, whose files Steam puts back when it updates or checks it."""
    if manifest["kind"] != "game" or not folder.strip():
        return False

    root = os.path.normcase(os.path.abspath(manifest["root"]))
    folder = os.path.normcase(os.path.abspath(folder))

    try:
        return os.path.commonpath([root, folder]) == root
    except ValueError:  # on different drives
        return False
