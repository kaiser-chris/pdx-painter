"""Tests of the plugin's core, which runs without Painter.

Run with the Python Painter ships, from the repository's root:

    "<Painter>/resources/pythonsdk/python.exe" -m unittest discover -s plugin/tests

The tests of running pdx-painter need it built into the plugin first (build.bat).
"""

import importlib
import json
import os
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "plugin"))

# Painter lists the plugin by its folder, whose name has a space, which only
# importlib takes.
core = importlib.import_module("PDX Painter.core")

TOOL = core.tool_path()


def manifest(kind="game", root="C:/Games/Victoria 3/game"):
    return {
        "version": 1,
        "kind": kind,
        "root": root,
        "textureSets": [
            {
                "name": "wall",
                "width": 1024,
                "height": 512,
                "maps": {},
                "channels": [
                    {"id": "basecolor", "color": True, "exportType": "documentMap", "exportName": "basecolor"},
                    {"id": "roughness", "color": False, "exportType": "documentMap", "exportName": "roughness"},
                ],
            },
            {"name": "roof", "width": 2048, "height": 2048, "channels": [], "maps": {}},
        ],
    }


class TestResolution(unittest.TestCase):
    def test_nearest_power_of_two_within_painter(self):
        self.assertEqual(core.resolution(512), 512)
        self.assertEqual(core.resolution(600), 512)
        self.assertEqual(core.resolution(800), 1024)
        self.assertEqual(core.resolution(16), core.MIN_RESOLUTION)
        self.assertEqual(core.resolution(100000), core.MAX_RESOLUTION)
        self.assertEqual(core.resolution(0), 1024)

    def test_largest(self):
        self.assertEqual(core.largest_resolution(manifest()), 2048)


class TestExportConfig(unittest.TestCase):
    def test_a_preset_per_texture_set_and_a_png_per_channel(self):
        config = core.export_config(manifest(), "C:/out")

        self.assertEqual(config["exportPath"], "C:/out")
        self.assertEqual([entry["rootPath"] for entry in config["exportList"]], ["wall", "roof"])
        self.assertEqual(config["exportList"][0]["exportPreset"], config["exportPresets"][0]["name"])

        maps = config["exportPresets"][0]["maps"]
        self.assertEqual([m["fileName"] for m in maps], ["$textureSet_basecolor", "$textureSet_roughness"])

        # Colour as R, G and B; grey as L.
        self.assertEqual([c["destChannel"] for c in maps[0]["channels"]], ["R", "G", "B"])
        self.assertEqual(maps[1]["channels"], [
            {"srcMapType": "documentMap", "srcMapName": "roughness", "destChannel": "L", "srcChannel": "L"}])

        parameters = config["exportParameters"][0]["parameters"]
        self.assertEqual((parameters["fileFormat"], parameters["bitDepth"]), ("png", "8"))

        # Painter takes it as JSON.
        json.dumps(config)


class TestWhereToWrite(unittest.TestCase):
    def test_a_mod_writes_into_itself_and_the_game_into_a_mod(self):
        self.assertEqual(core.default_out_root(manifest("mod", "D:/mods/mine")), "D:/mods/mine")
        self.assertEqual(core.default_out_root(manifest("loose", "D:/loose")), "D:/loose")
        self.assertEqual(core.default_out_root(manifest("game"), "D:/mods/last"), "D:/mods/last")
        self.assertEqual(core.default_out_root(manifest("game")), "")

    def test_a_mesh_starts_in_its_own_folder(self):
        self.assertEqual(core.default_out_root(manifest("mesh", "D:/work/house")), "D:/work/house")

    def test_overwriting_the_game_is_told(self):
        self.assertTrue(core.overwrites_game(manifest("game")))
        self.assertFalse(core.overwrites_game(manifest("mod", "D:/mods/mine")))
        self.assertFalse(core.overwrites_game(manifest("mesh", "D:/work")))

    def test_the_question_of_an_entity_and_of_a_mesh(self):
        entity = dict(manifest("mod", "D:/mods/mine"), entity="house_entity", asset="D:/mods/mine/house.asset")
        question = core.export_question(entity)
        self.assertEqual(question["inPlace"], "Overwrite")
        self.assertIn("house_entity", question["text"])
        self.assertIn("straight into it", question["details"])

        mesh = dict(manifest("mesh", "D:/work"), entity="house", asset="D:/work/house.mesh")
        mesh["textureSets"][0]["maps"] = {"diffuse": {"new": True}}
        question = core.export_question(mesh)
        self.assertEqual(question["inPlace"], "Save Next to the Mesh")
        self.assertIn("house.mesh", question["text"])
        self.assertIn("not there yet", question["details"])

    def test_every_game_has_its_names(self):
        for product in core.GAME_ORDER:
            self.assertIn(product, core.GAMES)
            self.assertIn(product, core.INSTALL_FOLDERS)

    def test_writing_into_the_game_is_told(self):
        game = manifest("game", "C:/Games/Victoria 3/game")
        self.assertTrue(core.writes_into_game(game, "C:/Games/Victoria 3/game/"))
        self.assertFalse(core.writes_into_game(game, "D:/mods/mine"))
        self.assertFalse(core.writes_into_game(manifest("mod", "D:/mods/mine"), "D:/mods/mine"))


class TestMetadata(unittest.TestCase):
    def test_none_is_left_out(self):
        self.assertEqual(core.for_metadata({"a": None, "b": [1, None, {"c": None, "d": "x"}]}),
                         {"b": [1, {"d": "x"}]})

    def test_versions(self):
        core.check_manifest({"version": core.MANIFEST_VERSION})
        with self.assertRaises(core.ToolError):
            core.check_manifest({"version": 99})


@unittest.skipUnless(os.path.isfile(TOOL), "pdx-painter is not built")
class TestRunTool(unittest.TestCase):
    def test_list(self):
        with tempfile.TemporaryDirectory() as folder:
            asset = os.path.join(folder, "loose.asset")
            with open(asset, "w", encoding="utf-8") as file:
                file.write('entity = { name = "b_entity" }\nentity = { name = "a_entity" }\n')

            listing = core.run_tool(["list", asset], TOOL)

        self.assertEqual(listing["entities"], ["a_entity", "b_entity"])
        self.assertEqual(listing["kind"], "loose")

    def test_errors_are_raised_with_the_tool_s_message(self):
        with self.assertRaises(core.ToolError) as raised:
            core.run_tool(["list", "Z:/no/such.asset"], TOOL)

        self.assertIn("such.asset", str(raised.exception))

    def test_a_missing_tool_is_said(self):
        with self.assertRaises(core.ToolError):
            core.run_tool(["version"], "Z:/no/pdx-painter.exe")


if __name__ == "__main__":
    unittest.main()
