# Executed by the owned Go adapter. No database/source writes.
import base64
import hashlib
import json
import pathlib
import sqlite3
import sys

MAX_SESSIONS = 128
MAX_BYTES = 128 << 20
MAX_SESSION_BYTES = 64 << 20


def canonical(value):
    def binary(v):
        if isinstance(v, bytes):
            return {"base64": base64.b64encode(v).decode("ascii")}
        raise TypeError("unsupported SQLite value")
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"), default=binary)


def text_content(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "\n".join(b["text"] for b in value if isinstance(b, dict)
                         and b.get("type") in ("text", "input_text", "output_text")
                         and isinstance(b.get("text"), str))
    return ""


def object_data(row):
    data = json.loads(row["data"])
    if not isinstance(data, dict):
        raise ValueError("native message data must be a JSON object")
    return data


def emit_message(table, row, role, content):
    if role not in ("user", "assistant"):
        return None
    text = text_content(content)
    if not text.strip():
        return None
    return {"type": "message", "timestamp": str(row.get("timestamp", row.get("time_created", ""))),
            "message": {"role": role, "content": text},
            "source": {"table": table, "message_id": str(row["id"])}}


def require(db, table, fields):
    columns = {r[1] for r in db.execute('PRAGMA table_info("' + table + '")')}
    if not set(fields) <= columns:
        raise ValueError("unsupported native schema: " + table + " requires " + ",".join(fields))


def read_rows(db, table, sid, order):
    return [dict(r) for r in db.execute('SELECT * FROM "' + table + '" WHERE session_id=? ORDER BY ' + order, (sid,))]


def layouts(db, tables, format):
    if format == "hermes":
        require(db, "sessions", ["id", "source", "started_at"])
        require(db, "messages", ["id", "session_id", "role", "content", "timestamp", "active", "compacted", "_compressed_summary"])
        return [("sessions", "messages", None, False)]
    found = []
    for st, mt, pt, v2 in [("session", "message", "part", False), ("session_v2", "session_message", "session_part", True)]:
        present = {st, mt, pt} & tables
        if present and present != {st, mt, pt}:
            raise ValueError("unsupported incomplete OpenCode schema: " + st)
        if not present:
            continue
        require(db, st, ["id", "time_updated"])
        require(db, mt, ["id", "session_id", "time_created", "time_updated", "data"] + (["type", "seq"] if v2 else []))
        require(db, pt, ["message_id", "session_id", "time_created", "time_updated", "data"] + (["ordinal", "type"] if v2 else ["id"]))
        found.append((st, mt, pt, v2))
    if not found:
        raise ValueError("unsupported OpenCode schema: no conversation tables")
    return found


def session_body(db, schema, format, sid):
    raw, messages, mirrored_text = [], [], {}
    for st, mt, pt, v2 in sorted(schema, key=lambda x: not x[3]):
        session = db.execute('SELECT * FROM "' + st + '" WHERE id=?', (sid,)).fetchone()
        if session is None:
            continue
        raw.append({"table": st, "row": dict(session)})
        order = "timestamp,id" if format == "hermes" else ("seq,id" if v2 else "time_created,id")
        rows = read_rows(db, mt, sid, order)
        by_message = {}
        if pt:
            for part in read_rows(db, pt, sid, "message_id,ordinal" if v2 else "message_id,time_created,id"):
                raw.append({"table": pt, "row": part})
                by_message.setdefault(part["message_id"], []).append(part)
        for row in rows:
            raw.append({"table": mt, "row": row})
            if format == "hermes":
                # Inactive/compacted originals remain evidence; generated summaries
                # are custodied but never treated as original semantic text.
                if row["_compressed_summary"]:
                    continue
                role, content = row["role"], row["content"]
                if isinstance(content, str) and content.lstrip().startswith("["):
                    try:
                        parsed = json.loads(content)
                        if isinstance(parsed, list):
                            content = parsed
                    except ValueError:
                        pass
            else:
                data = object_data(row)
                role = row["type"] if v2 else data.get("role")
                content = data.get("content", data.get("text", ""))
                if not text_content(content):
                    content = []
                    for part in by_message.get(row["id"], []):
                        block = object_data(part)
                        if v2:
                            block.setdefault("type", part["type"])
                        content.append(block)
            message = emit_message(mt, row, role, content)
            if message is None:
                continue
            signature = (role, message["message"]["content"])
            # Only dedupe across the two schema mirrors, never repeated turns
            # within one source history. All original rows remain above.
            if format == "opencode":
                if not v2 and mirrored_text.get(signature, 0):
                    mirrored_text[signature] -= 1
                    continue
                if v2:
                    mirrored_text[signature] = mirrored_text.get(signature, 0) + 1
            messages.append(message)
    messages.sort(key=lambda m: float(m["timestamp"] or 0))
    return raw, messages


def metadata_rows(db, table, sid, session=False):
    columns = {r[1] for r in db.execute('PRAGMA table_info("' + table + '")')}
    # No content, reasoning, tool payload or data JSON on the metadata path.
    wanted = [n for n in ("id", "message_id", "type", "seq", "ordinal", "role", "time_created",
                         "time_updated", "timestamp", "active", "compacted", "_compressed_summary",
                         "started_at", "ended_at", "runtime_revision", "runtime_generation",
                         "message_count", "rewind_count") if n in columns]
    selected = ",".join('"' + n + '"' for n in wanted)
    order = ",".join(str(i + 1) for i in range(len(wanted)))
    where = "id" if session else "session_id"
    return [dict(r) for r in db.execute('SELECT ' + selected + ' FROM "' + table + '" WHERE ' + where + '=? ORDER BY ' + order, (sid,))]


def inventory(db, schema):
    ids = sorted({str(r[0]) for st, _, _, _ in schema for r in db.execute('SELECT id FROM "' + st + '"')})
    for sid in ids:
        meta = []
        for st, mt, pt, _ in schema:
            sessions = metadata_rows(db, st, sid, True)
            if not sessions:
                continue
            meta.extend({"table": st, "row": r} for r in sessions)
            for table in (mt, pt):
                if table:
                    meta.extend({"table": table, "row": r} for r in metadata_rows(db, table, sid))
        yield sid, hashlib.sha256(canonical(meta).encode()).hexdigest(), len(meta)


def main():
    request = json.load(sys.stdin)
    db = sqlite3.connect(pathlib.Path(request["database"]).absolute().as_uri() + "?mode=ro", uri=True, timeout=5)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA query_only=ON")
    db.execute("BEGIN")
    tables = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    schema = layouts(db, tables, request["format"])
    for st, mt, pt, _ in schema:
        for table in (mt, pt):
            if table and db.execute('SELECT 1 FROM "' + table + '" AS m LEFT JOIN "' + st + '" AS s ON s.id=m.session_id WHERE s.id IS NULL LIMIT 1').fetchone():
                raise ValueError("native conversation rows have no owning session: " + table)
    count, size, pending = 0, 0, 0
    for sid, meta, metadata_count in inventory(db, schema):
        old = request["previous"].get(sid, "")
        result = {"session_id": sid, "metadata_rows": metadata_count}
        if old.startswith("native-session-v2:" + meta + ":") and (not request["reconcile"] or old.endswith(":" + str(request["reconcile_epoch"]))):
            print(canonical(result), flush=True)
            continue
        if count >= MAX_SESSIONS or size >= MAX_BYTES:
            pending += 1
            print(canonical(result), flush=True)
            continue
        rows, messages = session_body(db, schema, request["format"], sid)
        original = canonical(rows)
        result["body_rows"], result["body_bytes"] = len(rows), len(original.encode())
        fingerprint = "native-session-v2:" + meta + ":" + hashlib.sha256(original.encode()).hexdigest() + ":" + str(request["reconcile_epoch"])
        result["fingerprint"] = fingerprint
        count += 1
        if old.rsplit(":", 1)[0] != fingerprint.rsplit(":", 1)[0]:
            header = {"type": "native_session", "format": request["format"], "session_id": sid,
                      "tables": sorted({r["table"] for r in rows})}
            body = canonical(header) + "\n" + "".join(canonical({"type": "source_row", **r}) + "\n" for r in rows)
            body += "".join(canonical(m) + "\n" for m in messages)
            if len(body.encode()) > MAX_SESSION_BYTES:
                raise ValueError("native session exceeds 64 MiB; configure a bounded source export")
            if size + len(body.encode()) > MAX_BYTES:
                result.pop("fingerprint")
                pending += 1
            else:
                result["body"] = body
                size += len(body.encode())
        print(canonical(result), flush=True)
    db.rollback()
    db.close()
    print(canonical({"complete": True, "pending": pending}), flush=True)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        # Do not expose source bodies or SQL parameters in errors.
        if isinstance(exc, ValueError):
            print(str(exc), file=sys.stderr)
        else:
            print("native SQLite read failed (" + type(exc).__name__ + ")", file=sys.stderr)
        sys.exit(1)
