"""S452: Komga and Audiobookshelf get a default library on bulk storage at install."""
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def test_komga_declares_bulk_library_and_first_run_library():
    y = read("catalog/komga.yaml")
    assert "target: /library\n    class: bulk" in y
    assert "kind: comics" in y and "path: /library" in y
    assert "KOMGA_OIDCEMAILVERIFICATION" in y


def test_audiobookshelf_declares_bulk_libraries_and_first_run_libraries():
    y = read("catalog/audiobookshelf.yaml")
    for p in ("/audiobooks", "/podcasts"):
        assert f"target: {p}\n    class: bulk" in y
        assert f"path: {p}" in y
    assert "kind: audiobooks" in y and "kind: podcasts" in y


def test_schema_accepts_libraries_for_these_routines():
    s = read("internal/catalog/schema.go")
    assert "FirstRunKomga:" in s and "FirstRunAudiobookshelf:" in s


def test_routines_add_libraries_idempotently():
    assert "komgaLibraries" in read("internal/firstrun/komga.go")
    assert "audiobookshelfLibraries" in read("internal/firstrun/audiobookshelf.go")


def test_slice_doc_and_index():
    assert "ADR-059" in read("docs/slices/S452-komga-audiobookshelf-default-library.md")
    assert "S452" in read("docs/SLICE_INDEX.md")
