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


def export_config(manifest, export_path):
    """The configuration of Painter's export that writes every channel of the
    manifest the way pdx-painter's pack reads them: one PNG of eight bits per
    channel, named after the Texture Set and the channel.

    Every Texture Set gets a preset of its own, since not every set has every
    channel.
    """
    presets = []
    export_list = []

    for texture_set in manifest["textureSets"]:
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

        return {
            "text": "Where should the textures of {0} go?".format(os.path.basename(manifest["asset"])),
            "details": ("Next to the mesh, they are named the way its material names them"
                        + (", and the maps that are not there yet are made there." if new else ".")
                        + " In another folder, they go straight into it."),
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
