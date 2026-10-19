"""PDX Painter: open the entities of the Paradox games' .asset files, and their
.mesh files, in Substance 3D Painter, and write what is painted back into the
games' textures. It reads Victoria 3 and Europa Universalis 5, and Crusader
Kings 3, whose files are made alike.

The plugin adds a PDX Painter menu. Open Entity or Mesh has pdx-painter read
the file, creates a project from its mesh and lays its textures into the layer
stack. Export Textures exports the channels and has pdx-painter pack them into
the games' maps again, in the formats they were read in.

Painter lists the plugin by the name of this folder, which is why it has a
space in it.
"""

# The Painter side is imported when Painter starts the plugin, so that the
# core can be imported, and tested, without Painter.


def start_plugin():
    from . import plugin

    plugin.start()


def close_plugin():
    from . import plugin

    plugin.close()
