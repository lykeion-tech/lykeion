# Passes once the column's author has written its note.
def check(records):
    if [r for r in records if r["kind"] == "notes" and r["column"] == "grounding"]:
        return [{"id": "note", "ok": True}]
    return [{"id": "note", "ok": False, "failure": "missing_grounding_note"}]
