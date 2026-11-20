"""The 0.0.17 picture: ACDSeeN's context menu with "Upload to tibi", and its
status bar after the upload. Run by acdseen.sh with ACDSeeN's own Python;
TIBI_SERVER / TIBI_TOKEN point at a real tibi server.

    python acdseen.py ACDSEEN_DIR PICTURES_DIR OUT_DIR
"""
import os
import sys
import time
from pathlib import Path

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
acd, pics, out = (Path(a) for a in sys.argv[1:4])
sys.path.insert(0, str(acd))

from PySide6.QtCore import QPoint  # noqa: E402
from PySide6.QtWidgets import QApplication  # noqa: E402

from acdseen.browser import Browser  # noqa: E402

app = QApplication([])


def pump(ms, until=None):
    end = time.monotonic() + ms / 1000
    while time.monotonic() < end:
        app.processEvents()
        if until is not None and until():
            return True
        time.sleep(0.005)
    return until is None


b = Browser(pics)
b.resize(1000, 620)
b.show()
pump(4000)
v = b._view
idx = [b._model.index(b._model.index_of(p), 0) for p in b._model.paths()[:3]]
v.setCurrentIndex(idx[0])
for i in idx:
    v.selectionModel().select(i, v.selectionModel().SelectionFlag.Select)
pump(300)
pos = v.visualRect(idx[1]).center()
menu = b._build_file_menu(pos)
menu.popup(v.viewport().mapToGlobal(pos))
pump(300)
menu.setActiveAction(next(a for a in menu.actions() if "tibi" in a.text()))  # as if hovered
pump(300)
b.grab().save(str(out / "window.png"))
menu.grab().save(str(out / "menu.png"))
at = v.viewport().mapTo(b, pos)
# The folder tree shows this machine's directories: keep only the right side.
x0 = b._view.mapTo(b, QPoint(0, 0)).x() - 4
(out / "menu-at").write_text(f"{at.x()} {at.y()} {x0}\n")
menu.close()

fut = b._upload_tibi()
assert pump(60000, fut.done), "upload did not finish"
pump(500)
print(fut.result())
after = b.grab()
sb = b._status
after.copy(0, sb.y(), b.width(), sb.height()).save(str(out / "status.png"))
b.close()
