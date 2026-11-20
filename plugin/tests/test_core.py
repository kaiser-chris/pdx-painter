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

# The folder the plugin is in, next to this one: plugin/ in the repository,
# the folder of the bundle in a release build.
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

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


def opened(kind="game", root="C:/Games/Victoria 3/game"):
    """A manifest of two Texture Sets, as prepare writes one."""
    return {
        "version": 1,
        "kind": kind,
        "root": root,
        "entity": "house_entity" if kind != "mesh" else "house",
        "asset": root + "/gfx/models/house/house." + ("mesh" if kind == "mesh" else "asset"),
        "meshFile": {"path": root + "/gfx/models/house/house_model.mesh", "relative": "gfx/models/house/house_model.mesh"},
        "textureSets": [
            {"name": "house", "meshes": [{"shape": "houseShape", "index": 0}], "channels": [], "maps": {
                "diffuse": {"relative": "gfx/models/house/house_diffuse.dds", "format": "BC1"},
                "normal": {"relative": "gfx/models/house/house_normal.dds", "format": "BC3"},
                "properties": {"relative": "gfx/models/house/house_properties.dds", "format": "BC3"},
            }},
            {"name": "decal", "meshes": [], "channels": [], "maps": {
                "diffuse": {"relative": "gfx/models/shared/decal.png", "format": "PNG"},
            }},
        ],
    }


class TestAdvancedExport(unittest.TestCase):
    def test_defaults_of_an_entity_and_of_a_mesh(self):
        entity = opened()
        self.assertEqual(core.default_name(entity), "house")
        self.assertFalse(core.derives_by_default(entity))
        self.assertTrue(core.derives_by_default(opened("mesh")))
        self.assertEqual(core.default_folder(entity), "C:/Games/Victoria 3/game/gfx/models/house")

    def test_the_names_of_what_was_opened(self):
        names = core.existing_names(opened())
        self.assertEqual(names["asset"], "house.asset")
        self.assertEqual(names["mesh"], "house_model.mesh")
        self.assertEqual(names["maps"][("house", "diffuse")], "house_diffuse.dds")
        self.assertEqual(names["maps"][("decal", "diffuse")], "decal.png")

    def test_names_derived_from_one(self):
        names = core.derived_names(opened(), "villa")
        self.assertEqual((names["asset"], names["mesh"]), ("villa.asset", "villa.mesh"))
        self.assertEqual(names["maps"][("house", "normal")], "villa_normal.dds")

        # The second set is numbered, and keeps the PNG it was read as, until
        # another format is chosen.
        self.assertEqual(names["maps"][("decal", "diffuse")], "villa_2_diffuse.png")
        chosen = core.derived_names(opened(), "villa", {("decal", "diffuse"): "BC7"})
        self.assertEqual(chosen["maps"][("decal", "diffuse")], "villa_2_diffuse.dds")

    def test_the_definitions_of_an_asset(self):
        self.assertEqual(core.asset_definitions("villa.asset"), ("villa_mesh", "villa_entity"))

    def test_what_can_be_written(self):
        self.assertTrue(core.can_write_asset(opened()))
        unknown = dict(opened(), meshFile=None)
        self.assertFalse(core.can_write_mesh(unknown))
        self.assertFalse(core.can_write_asset(unknown))

    def test_a_plan_and_its_problems(self):
        plan = core.build_plan("D:/mods/villa", asset="villa.asset", mesh="villa.mesh", maps=[
            ("house", "diffuse", "villa_diffuse.dds", "BC7", ".dds"),
            ("house", "normal", "villa_normal.dds", "", ".dds"),
        ])

        self.assertEqual(plan["asset"], {"file": "villa.asset", "mesh": "villa_mesh", "entity": "villa_entity"})
        self.assertEqual(plan["maps"][0]["format"], "BC7")
        self.assertNotIn("format", plan["maps"][1])
        self.assertEqual(core.plan_problems(plan), [])

        bad = core.build_plan("", mesh="villa.obj", maps=[
            ("house", "diffuse", "same.dds", "", ".dds"),
            ("house", "normal", "SAME.dds", "", ".dds"),
            ("house", "properties", "", "", ".dds"),
        ])
        problems = " ".join(core.plan_problems(bad))
        for expected in ["Choose a folder", "is to end in .mesh", "same.dds is chosen for two files", "Give the properties map"]:
            self.assertIn(expected, problems)

        self.assertEqual(core.plan_problems(core.build_plan("D:/x")), ["Choose something to export."])

    def test_files_written_over(self):
        with tempfile.TemporaryDirectory() as folder:
            open(os.path.join(folder, "villa_diffuse.dds"), "wb").close()
            plan = core.build_plan(folder, maps=[
                ("house", "diffuse", "villa_diffuse.dds", "", ".dds"),
                ("house", "normal", "villa_normal.dds", "", ".dds"),
            ])

            self.assertEqual(core.existing_files(plan), ["villa_diffuse.dds"])

    def test_inside_the_game(self):
        entity = opened()
        self.assertTrue(core.inside_game(entity, "C:/Games/Victoria 3/game/gfx/models/house"))
        self.assertFalse(core.inside_game(entity, "D:/mods/villa"))
        self.assertFalse(core.inside_game(opened("mod", "D:/mods/mine"), "D:/mods/mine/gfx"))

    def test_only_the_sets_chosen_are_exported(self):
        config = core.export_config(opened(), "C:/out", only={"decal"})
        self.assertEqual([entry["rootPath"] for entry in config["exportList"]], ["decal"])


class TestFormats(unittest.TestCase):
    def test_keep_names_the_format_read(self):
        self.assertEqual(core.keep_label({"format": "BC3_UNORM"}), "Keep (DXT5 (BC3))")
        self.assertEqual(core.keep_label({"format": "BC7_UNORM_SRGB"}), "Keep (BC7 sRGB)")
        self.assertEqual(core.keep_label({"format": "Legacy"}), "Keep (legacy layout)")
        self.assertEqual(core.keep_label({"format": "R8G8_UNORM"}), "Keep (R8G8_UNORM)")

        # As a project opened by the first version names them.
        self.assertEqual(core.keep_label({"format": "BC3"}), "Keep (DXT5 (BC3))")

        # A map that is not there yet is made in its format.
        self.assertEqual(core.keep_label({"format": "BC3_UNORM", "new": True}), "Default (DXT5 (BC3))")

    def test_the_first_manifests_are_read(self):
        core.check_manifest({"version": 1})
        core.check_manifest({"version": 2})


@unittest.skipUnless(os.path.isfile(TOOL), "pdx-painter is not built")
class TestFormatsOfTheTool(unittest.TestCase):
    def test_every_format_offered_is_one_the_tool_writes(self):
        writable = set(core.run_tool(["formats"], TOOL)["formats"])

        for value, label in core.FORMATS:
            if value:
                self.assertIn(value, writable, label)


def foreign(sets=("wall", "roof")):
    """The manifest pdx-painter prepares of a project of the user's own."""
    with tempfile.TemporaryDirectory() as folder:
        args = ["prepare", "-foreign", "house", "-out", folder]
        for name in sets:
            args += ["-set", name]

        return core.run_tool(args, TOOL)


@unittest.skipUnless(os.path.isfile(TOOL), "pdx-painter is not built")
class TestForeignProjects(unittest.TestCase):
    def test_a_foreign_project_starts_deriving_and_can_write_a_mesh(self):
        manifest = foreign()
        self.assertTrue(core.is_foreign(manifest))
        self.assertTrue(core.derives_by_default(manifest))
        self.assertTrue(core.can_write_mesh(manifest))
        self.assertTrue(core.can_write_asset(manifest))
        self.assertEqual(core.default_name(manifest), "house")

        names = core.existing_names(manifest)
        self.assertEqual(names["asset"], "house.asset")
        self.assertEqual(names["mesh"], "house.mesh")
        self.assertEqual(names["maps"][("roof", "normal")], "house_2_normal.dds")

    def test_channels_come_from_what_the_project_has(self):
        available = {
            "wall": {"BaseColor": "", "Normal": "", "Metallic": "", "Roughness": "", "Opacity": ""},
            "roof": {"BaseColor": "", "Normal": "", "User0": "Diffuse alpha", "User1": "Height mask", "Opacity": ""},
        }

        manifest = core.foreign_channels(foreign(), available)
        wall, roof = manifest["textureSets"]

        sources = {channel["id"]: (channel["painter"], channel["exportName"]) for channel in wall["channels"]}
        self.assertEqual(sources["diffusealpha"], ("Opacity", "opacity"))
        self.assertEqual(sources["normal"], ("Normal", "Normal_DirectX"))
        self.assertNotIn("subsurface", sources)

        sources = {channel["id"]: channel["painter"] for channel in roof["channels"]}
        self.assertEqual(sources["diffusealpha"], "User0")

        # A user channel labelled otherwise is the project's own, not ours.
        self.assertNotIn("subsurface", sources)
        self.assertNotIn("metallic", sources)

        # Painter takes it as JSON.
        json.dumps(core.export_config(manifest, "C:/out"))

    def test_the_channels_of_the_plugin_a_project_lacks(self):
        missing, taken = core.missing_pdx_channels({
            "wall": {"User0": "Diffuse alpha"},
            "roof": {"User1": "Height mask"},
        })

        self.assertEqual([type_name for type_name, _, _ in missing["wall"]], ["User1", "User2"])
        self.assertEqual([type_name for type_name, _, _ in missing["roof"]], ["User0", "User2"])
        self.assertEqual(taken, [("roof", "User1", "Height mask")])


class TestForeignPlans(unittest.TestCase):
    def test_a_mesh_made_of_an_obj(self):
        plan = core.build_plan("D:/out", asset="house.asset", mesh="house.mesh", obj="C:/work/painter.obj", scale=0.01)
        self.assertEqual(plan["mesh"], {"file": "house.mesh", "obj": "C:/work/painter.obj", "scale": 0.01})
        self.assertNotIn("meshFile", plan["asset"])

    def test_an_asset_of_a_mesh_of_the_users(self):
        plan = core.build_plan("D:/out", asset="house.asset", asset_mesh="my_house.mesh")
        self.assertEqual(plan["asset"]["meshFile"], "my_house.mesh")
        self.assertEqual(core.plan_problems(plan), [])

        plan = core.build_plan("D:/out", asset="house.asset", asset_mesh="")
        self.assertIn("Mesh row", " ".join(core.plan_problems(plan)))


if __name__ == "__main__":
    unittest.main()
