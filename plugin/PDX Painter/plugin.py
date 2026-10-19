"""The Painter side of the plugin: its menu, opening an entity or a mesh, and
exporting the textures."""

import hashlib
import json
import os
import shutil
import tempfile

try:
    from PySide6 import QtCore, QtGui, QtWidgets
except ImportError:  # Painter 9 and older
    from PySide2 import QtCore, QtGui, QtWidgets

import substance_painter.colormanagement
import substance_painter.event
import substance_painter.export
import substance_painter.layerstack
import substance_painter.logging
import substance_painter.project
import substance_painter.resource
import substance_painter.textureset
import substance_painter.ui

from . import core

TITLE = "PDX Painter"

# The UI the plugin added, removed when it is closed.
_elements = []

# The manifest of a project being created, which is laid into its layer stack
# once Painter has it ready.
_pending = None


# --- Settings ---------------------------------------------------------------


def _settings():
    return QtCore.QSettings("pdx-painter", "pdx-painter")


def _setting(key, default=""):
    value = _settings().value(key, default)
    if isinstance(default, bool):
        return value in (True, "true", "1", 1)

    return value or default


def _set_setting(key, value):
    _settings().setValue(key, value)


def _game_folder(product):
    """The game folder set for a game. The one setting of the first version,
    which was Victoria 3's, counts as Victoria 3's."""
    folder = _setting("game_folder_" + product)
    if not folder and product == core.VICTORIA_3:
        folder = _setting("game_folder")

    return folder


# --- Plugin lifetime ----------------------------------------------------------


def start():
    window = substance_painter.ui.get_main_window()

    menu = QtWidgets.QMenu(TITLE, window)
    menu.addAction("Open Entity or Mesh...").triggered.connect(_guarded(open_file))
    menu.addAction("Export Textures...").triggered.connect(_guarded(export_textures))
    menu.addSeparator()
    menu.addAction("Settings...").triggered.connect(_guarded(edit_settings))

    substance_painter.ui.add_menu(menu)
    _elements.append(menu)

    substance_painter.event.DISPATCHER.connect_strong(substance_painter.event.ProjectEditionEntered, _on_edition_entered)


def close():
    substance_painter.event.DISPATCHER.disconnect(substance_painter.event.ProjectEditionEntered, _on_edition_entered)

    for element in _elements:
        substance_painter.ui.delete_ui_element(element)

    _elements.clear()


def _guarded(action):
    """An action of the menu that shows what went wrong rather than leaving it
    in the log."""

    def run(*_):
        try:
            action()
        except core.ToolError as error:
            _error(str(error))
        except Exception as error:  # pylint: disable=broad-except
            substance_painter.logging.error("PDX Painter: {0!r}".format(error))
            _error("{0}: {1}".format(type(error).__name__, error))
        finally:
            while QtWidgets.QApplication.overrideCursor() is not None:
                QtWidgets.QApplication.restoreOverrideCursor()

    return run


def _window():
    return substance_painter.ui.get_main_window()


def _error(text):
    QtWidgets.QMessageBox.critical(_window(), TITLE, text)


def _busy():
    QtWidgets.QApplication.setOverrideCursor(QtGui.QCursor(QtCore.Qt.WaitCursor))
    QtWidgets.QApplication.processEvents()


def _idle():
    QtWidgets.QApplication.restoreOverrideCursor()


def _run(args):
    _busy()
    try:
        return core.run_tool(args, _setting("tool"))
    finally:
        _idle()


# --- Opening ------------------------------------------------------------------


def open_file():
    global _pending

    if not _close_project():
        return

    path, _ = QtWidgets.QFileDialog.getOpenFileName(
        _window(), "Open an entity or a mesh", _setting("last_folder"),
        "Entities and meshes (*.asset *.mesh);;Asset files (*.asset);;Mesh files (*.mesh)")
    if not path:
        return

    _set_setting("last_folder", os.path.dirname(path))

    if path.lower().endswith(".mesh"):
        args = _mesh_args(path)
    else:
        args = _entity_args(path)

    if args is None:
        return

    if _setting("flip_green", False):
        args.append("-flip-green")

    manifest = _run(args)
    core.check_manifest(manifest)

    settings = substance_painter.project.Settings(
        normal_map_format=substance_painter.project.NormalMapFormat.DirectX,
        default_texture_resolution=core.largest_resolution(manifest),
    )

    # The layers are laid once Painter says the new project is ready.
    _pending = manifest
    try:
        substance_painter.project.create(manifest["mesh"], settings=settings)
    except Exception:
        _pending = None
        raise


def _entity_args(asset):
    """The arguments that prepare an entity of an asset file, after asking
    which, or None if the user gave up."""
    listing = _run(["list", asset])
    entities = listing["entities"]

    if not entities:
        _error("{0} defines no entity.".format(os.path.basename(asset)))
        return None

    entity = entities[0]
    if len(entities) > 1:
        entity, chosen = QtWidgets.QInputDialog.getItem(
            _window(), TITLE, "The file defines {0} entities. Open:".format(len(entities)), entities, 0, False)
        if not chosen:
            return None

    folder = _work_folder(asset, entity)
    shutil.rmtree(folder, ignore_errors=True)

    args = ["prepare", "-asset", asset, "-entity", entity, "-out", folder]

    if listing["kind"] == "mod":
        game = _game_of_mod(listing.get("product", ""))
        if not game:
            return None

        args += ["-game", game]

    if _setting("attachments", False):
        args.append("-attachments")

    return args


def _mesh_args(mesh):
    folder = _work_folder(mesh, "mesh")
    shutil.rmtree(folder, ignore_errors=True)

    return ["prepare", "-mesh", mesh, "-out", folder]


def _game_of_mod(product):
    """The game folder a mod is read with: the one set for the game it says it
    is for, asked for where none is set, and the game itself asked for where
    the mod does not say."""
    if product not in core.GAMES:
        names = [core.GAMES[key] for key in core.GAME_ORDER]
        name, chosen = QtWidgets.QInputDialog.getItem(
            _window(), TITLE, "The mod does not say which game it is for. It is for:", names, 0, False)
        if not chosen:
            return ""

        product = core.GAME_ORDER[names.index(name)]

    folder = _game_folder(product)
    if folder:
        return folder

    QtWidgets.QMessageBox.information(
        _window(), TITLE,
        "The file is part of a mod, which is read together with its game.\n\n"
        "Choose the game folder of {0}, such as\n...\\steamapps\\common\\{1}\\game".format(
            core.GAMES[product], core.INSTALL_FOLDERS[product]))

    folder = QtWidgets.QFileDialog.getExistingDirectory(_window(), "The game folder of " + core.GAMES[product])
    if folder:
        _set_setting("game_folder_" + product, folder)

    return folder


def _close_project():
    """Close the open project, asking first about unsaved changes. False if the
    user would rather keep it."""
    project = substance_painter.project
    if not project.is_open():
        return True

    if project.needs_saving():
        answer = QtWidgets.QMessageBox.question(
            _window(), TITLE, "The open project has unsaved changes. Close it anyway?",
            QtWidgets.QMessageBox.Yes | QtWidgets.QMessageBox.No, QtWidgets.QMessageBox.No)
        if answer != QtWidgets.QMessageBox.Yes:
            return False

    project.close()

    return True


def _work_folder(path, name):
    """Where a file is prepared: a folder of its own that outlives the
    project, since exporting reads the coverage of its UVs from there."""
    base = QtCore.QStandardPaths.writableLocation(QtCore.QStandardPaths.GenericDataLocation)
    key = hashlib.sha1(os.path.normcase(os.path.abspath(path)).encode("utf-8")).hexdigest()[:8]

    return os.path.join(base, "pdx-painter", "{0}_{1}".format(name, key))


def _on_edition_entered(_event):
    global _pending

    if _pending is None:
        return

    manifest, _pending = _pending, None
    substance_painter.project.execute_when_not_busy(_guarded(lambda: _lay_textures(manifest)))


def _lay_textures(manifest):
    """Lay the textures into the new project: the channels each Texture Set
    needs, a fill layer holding the maps that are there, and one holding what
    new maps start at."""
    textureset = substance_painter.textureset
    layerstack = substance_painter.layerstack

    substance_painter.project.Metadata(core.METADATA_CONTEXT).set("manifest", core.for_metadata(manifest))

    sets = {texture_set.name(): texture_set for texture_set in textureset.all_texture_sets()}
    missing = []

    with layerstack.ScopedModification("Lay in the game's textures"):
        for described in manifest["textureSets"]:
            texture_set = sets.get(described["name"])
            if texture_set is None:
                missing.append(described["name"])
                continue

            try:
                texture_set.set_resolution(textureset.Resolution(
                    core.resolution(described["width"]), core.resolution(described["height"])))
            except ValueError:
                size = core.resolution(max(described["width"], described["height"]))
                texture_set.set_resolution(textureset.Resolution(size, size))

            stack = texture_set.get_stack()
            images, starts = [], []

            for channel in described["channels"]:
                channel_type = getattr(textureset.ChannelType, channel["painter"])
                channel_format = getattr(textureset.ChannelFormat, channel["format"])
                label = channel.get("label") or None

                if not stack.has_channel(channel_type):
                    stack.add_channel(channel_type, channel_format, label)
                elif label:
                    stack.edit_channel(channel_type, channel_format, label)

                if channel.get("image"):
                    imported = substance_painter.resource.import_project_resource(
                        channel["image"], substance_painter.resource.Usage.TEXTURE,
                        name="{0}_{1}".format(described["name"], channel["id"]), group="pdx-painter")
                    images.append((channel_type, imported.identifier()))
                elif channel.get("default") is not None:
                    value = float(channel["default"])
                    starts.append((channel_type, substance_painter.colormanagement.Color(value, value, value)))

            _fill(stack, "Starting values", starts)
            _fill(stack, "Game original", images)

    lines = ["Opened {0} with {1} Texture Set(s).".format(manifest["entity"], len(manifest["textureSets"]))]

    new = [stored["relative"] for described in manifest["textureSets"]
           for stored in described["maps"].values() if stored.get("new")]
    if new:
        lines.append("\nThese maps are not there yet and are made on export:\n" + "\n".join(new))
    if missing:
        lines.append("\nPainter made no Texture Set of: " + ", ".join(missing))
    if manifest.get("diagnostics"):
        lines.append("\nWhat the game files said:\n" + "\n".join(manifest["diagnostics"][:20]))
    lines.append("\nPaint on new layers on top, then use PDX Painter > Export Textures.")

    QtWidgets.QMessageBox.information(_window(), TITLE, "\n".join(lines))


def _fill(stack, name, sources):
    """Add a fill layer to a stack that fills channels from sources: images
    or uniform colours, each a pair of channel and source."""
    if not sources:
        return

    layerstack = substance_painter.layerstack

    fill = layerstack.insert_fill(layerstack.InsertPosition.from_textureset_stack(stack))
    fill.set_name(name)
    fill.active_channels = {channel_type for channel_type, _ in sources}

    for channel_type, source in sources:
        fill.set_source(channel_type, source)


# --- Exporting ----------------------------------------------------------------


def _metadata_value(metadata, key):
    """A value of the project's metadata, or None where there is none."""
    try:
        return metadata.get(key)
    except Exception:  # pylint: disable=broad-except
        return None


def export_textures():
    project = substance_painter.project
    if not project.is_open():
        _error("No project is open.")
        return

    metadata = project.Metadata(core.METADATA_CONTEXT)
    manifest = _metadata_value(metadata, "manifest")
    if not manifest:
        _error("This project was not opened with PDX Painter > Open Entity or Mesh, so there is nowhere to write its textures to.")
        return

    core.check_manifest(manifest)

    where = _ask_where(manifest, metadata)
    if where is None:
        return

    exported = tempfile.mkdtemp(prefix="pdx-painter-")
    try:
        _busy()
        try:
            result = substance_painter.export.export_project_textures(core.export_config(manifest, exported))
        finally:
            _idle()

        if result.status != substance_painter.export.ExportStatus.Success:
            _error("Painter's export did not finish: {0}".format(result.message))
            return

        manifest_file = os.path.join(exported, "manifest.json")
        with open(manifest_file, "w", encoding="utf-8") as file:
            json.dump(manifest, file)

        packed = _run(["pack", "-manifest", manifest_file, "-exported", exported] + where)
    finally:
        shutil.rmtree(exported, ignore_errors=True)

    lines = []
    if packed["written"]:
        lines.append("Written:\n" + "\n".join(packed["written"]))
    if packed["unchanged"]:
        lines.append("Unchanged, so not written again:\n" + "\n".join(packed["unchanged"]))
    if packed["notes"]:
        lines.append("Notes:\n" + "\n".join(packed["notes"]))

    QtWidgets.QMessageBox.information(_window(), TITLE, "\n\n".join(lines) or "Nothing to write.")


def _choose(text, details, labels):
    """Ask a question with a button for each answer, and return the number of
    the one clicked, or None for the dialog closed. A dialog of its own rather
    than a QMessageBox, whose buttons Painter's style cuts the labels of short:
    each button here is as wide as its label."""
    dialog = QtWidgets.QDialog(_window())
    dialog.setWindowTitle(TITLE)
    dialog.setMinimumWidth(500)

    layout = QtWidgets.QVBoxLayout(dialog)

    heading = QtWidgets.QLabel(text)
    font = heading.font()
    font.setBold(True)
    heading.setFont(font)
    heading.setWordWrap(True)
    layout.addWidget(heading)

    body = QtWidgets.QLabel(details)
    body.setWordWrap(True)
    layout.addWidget(body)
    layout.addSpacing(8)

    row = QtWidgets.QHBoxLayout()
    row.addStretch(1)

    chosen = []

    for number, label in enumerate(labels):
        button = QtWidgets.QPushButton(label)
        button.setMinimumWidth(button.fontMetrics().horizontalAdvance(label) + 32)
        button.clicked.connect(lambda _=False, number=number: (chosen.append(number), dialog.accept()))
        if number == 0:
            button.setDefault(True)
        row.addWidget(button)

    layout.addLayout(row)
    dialog.exec()

    return chosen[0] if chosen else None


def _ask_where(manifest, metadata):
    """Ask where the textures go: over the files they came from, or next to
    the mesh for a mesh of its own, or into another folder. The arguments of
    pack that say so, or None if the user gave up."""
    question = core.export_question(manifest)
    clicked = _choose(question["text"], question["details"], [question["inPlace"], question["elsewhere"], "Cancel"])

    in_place, elsewhere = 0, 1

    if clicked == in_place:
        if core.overwrites_game(manifest):
            answer = QtWidgets.QMessageBox.warning(
                _window(), TITLE,
                "This writes over the game's own files. Steam puts them back when it updates or checks the game; "
                "a mod is the safer place. Write over them anyway? A .bak is kept of every file written over.",
                QtWidgets.QMessageBox.Yes | QtWidgets.QMessageBox.No, QtWidgets.QMessageBox.No)
            if answer != QtWidgets.QMessageBox.Yes:
                return None

        return ["-in-place"]

    if clicked != elsewhere:
        return None

    remembered = _metadata_value(metadata, "out") or ""
    default = remembered or core.default_out_root(manifest, _setting("out_folder"))

    out_root = QtWidgets.QFileDialog.getExistingDirectory(_window(), question["folderTitle"], default)
    if not out_root:
        return None

    if core.writes_into_game(manifest, out_root):
        answer = QtWidgets.QMessageBox.warning(
            _window(), TITLE,
            "That is the game's own folder. Steam puts its files back when it updates or checks the game. "
            "Write into it anyway?",
            QtWidgets.QMessageBox.Yes | QtWidgets.QMessageBox.No, QtWidgets.QMessageBox.No)
        if answer != QtWidgets.QMessageBox.Yes:
            return None

    metadata.set("out", out_root)
    if manifest["kind"] == "game":
        _set_setting("out_folder", out_root)

    return ["-out", out_root]


# --- Settings dialog ------------------------------------------------------------


def edit_settings():
    dialog = QtWidgets.QDialog(_window())
    dialog.setWindowTitle(TITLE + " settings")
    form = QtWidgets.QFormLayout(dialog)

    games = {}
    for product in core.GAME_ORDER:
        line = QtWidgets.QLineEdit(_game_folder(product))
        line.setPlaceholderText("...\\steamapps\\common\\{0}\\game".format(core.INSTALL_FOLDERS[product]))
        browse = QtWidgets.QPushButton("Browse...")

        def choose(_=False, line=line, product=product):
            folder = QtWidgets.QFileDialog.getExistingDirectory(
                dialog, "The game folder of " + core.GAMES[product], line.text())
            if folder:
                line.setText(folder)

        browse.clicked.connect(choose)

        row = QtWidgets.QHBoxLayout()
        row.addWidget(line)
        row.addWidget(browse)
        form.addRow(core.GAMES[product] + ":", row)
        games[product] = line

    form.addRow(QtWidgets.QLabel("The game folders are what mods are read with."))

    attachments = QtWidgets.QCheckBox("Open the entities attached to an entity as well")
    attachments.setChecked(_setting("attachments", False))
    form.addRow(attachments)

    flip = QtWidgets.QCheckBox("Flip the green of normal maps (if the bumps show inverted)")
    flip.setChecked(_setting("flip_green", False))
    form.addRow(flip)

    tool = QtWidgets.QLineEdit(_setting("tool"))
    tool.setPlaceholderText(core.tool_path())
    form.addRow("pdx-painter executable:", tool)

    buttons = QtWidgets.QDialogButtonBox(QtWidgets.QDialogButtonBox.Ok | QtWidgets.QDialogButtonBox.Cancel)
    buttons.accepted.connect(dialog.accept)
    buttons.rejected.connect(dialog.reject)
    form.addRow(buttons)

    # Wide enough to show a game folder whole.
    dialog.resize(650, dialog.sizeHint().height())

    if dialog.exec() != QtWidgets.QDialog.Accepted:
        return

    for product, line in games.items():
        _set_setting("game_folder_" + product, line.text().strip())

    # The setting of the first version is one of these now.
    _settings().remove("game_folder")

    _set_setting("attachments", attachments.isChecked())
    _set_setting("flip_green", flip.isChecked())
    _set_setting("tool", tool.text().strip())
