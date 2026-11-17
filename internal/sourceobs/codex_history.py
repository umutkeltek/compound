# Owned read-only SQLite exporter. Never changes native databases or rollouts.
import base64
import hashlib
import json
import pathlib
import sqlite3
import sys

WIDTH = 512
LARGE = 64 << 10
MAX_PACKS = 32
MAX_BYTES = 128 << 20
MAX_ROW = 64 << 20
MAX_THREADS = 16384
TABLES = ("thread_items", "thread_turns", "thread_realtime_items")
VISIBLE = ("userMessage", "agentMessage")
def canonical(v):
    def binary(x):
        if isinstance(x, bytes):
            return {"base64": base64.b64encode(x).decode("ascii")}
        raise TypeError("unsupported SQLite value")
    return json.dumps(v, sort_keys=True, ensure_ascii=False, separators=(",", ":"), default=binary)
def sha(s):
    return "sha256:" + hashlib.sha256(s.encode()).hexdigest()
def require(db, table, columns, schema="main"):
    found = {r[1] for r in db.execute('PRAGMA '+schema+'.table_info("'+table+'")')}
    if not set(columns) <= found:
        raise ValueError("unsupported Codex history schema: " + table)
    return sorted(found)
def text_content(v):
    if isinstance(v, str):
        return v
    if not isinstance(v, list):
        return ""
    return "\n".join(x["text"] for x in v if isinstance(x, dict)
                     and x.get("type") in ("text", "input_text", "output_text")
                     and isinstance(x.get("text"), str))
def message(row):
    item = json.loads(row["item_json"])
    if not isinstance(item, dict):
        raise ValueError("native item_json must be an object")
    kind = item.get("type")
    if kind == "userMessage":
        role, text = "user", text_content(item.get("content"))
    elif kind == "agentMessage":
        role, text = "assistant", item.get("text", "")
    else:
        return None
    if not isinstance(text, str) or not text.strip():
        return None
    return {"type": "message", "timestamp": str(row.get("created_at_ms", "")),
            "message": {"role": role, "content": text},
            "source": {"table": "thread_items" if "turn_id" in row else "thread_realtime_items",
                       "message_id": canonical([row.get("turn_id"), row["item_id"]])}}
def main():
    req = json.load(sys.stdin)
    db = sqlite3.connect(pathlib.Path(req["database"]).as_uri()+"?mode=ro", uri=True, timeout=5)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA query_only=ON")
    require(db, "thread_history_projection_state", ["thread_id", "next_rollout_byte_offset", "next_rollout_ordinal"])
    columns = {}
    columns["thread_items"] = require(db, "thread_items", ["thread_id", "turn_id", "item_id", "rollout_ordinal", "item_json", "item_type", "updated_at_ordinal", "created_at_ms"])
    columns["thread_turns"] = require(db, "thread_turns", ["thread_id", "turn_id", "rollout_ordinal", "status", "error_json"])
    columns["thread_realtime_items"] = require(db, "thread_realtime_items", ["thread_id", "item_id", "rollout_ordinal", "item_json", "item_type", "created_at_ms"])
    metadata_db = req.get("metadata_database")
    if metadata_db:
        db.execute("ATTACH DATABASE ? AS metadata", (pathlib.Path(metadata_db).as_uri()+"?mode=ro",))
        metadata_columns = require(db, "threads", ["id", "cwd", "title"], "metadata")
    db.execute("BEGIN")
    previous = req["previous"]
    states = req["states"]
    known = req["known_packs"]
    epoch = req["epoch"]
    stats = {"metadata_rows": 0, "body_rows": 0, "body_bytes": 0}
    used_packs = 0
    used_bytes = 0
    pending = 0
    deferred = 0
    seen = []
    body_threads = set()
    pack_metadata_rows = 0
    emitted = set()
    def emit(key, sid, kind, table, body, fingerprint=None, rows=0, bucket=None):
        if key in emitted:
            return
        emitted.add(key)
        body = canonical(body)+"\n" if not isinstance(body, str) else body
        print(canonical({"key":key,"thread_id":sid,"kind":kind,"table":table,"bucket":bucket,
                         "body":body,"fingerprint":fingerprint or "codex-body-v1:"+sha(body),
                         "rows":rows}), flush=True)
    def old(key):
        return previous.get(key, {})
    def stamp(meta):
        return sha(canonical(meta))
    def row_key(table, row):
        pk = [row["turn_id"]] if table == "thread_turns" else [row.get("turn_id"), row["item_id"]]
        return "large/"+row["thread_id"]+"/"+table+"/"+canonical(pk)
    def metadata(table, sid, bucket):
        nonlocal pack_metadata_rows
        body_field = "error_json" if table == "thread_turns" else "item_json"
        selected = [c for c in columns[table] if c != body_field]
        sql = ",".join('CASE WHEN typeof("'+c+'") IN (\'text\',\'blob\') AND length(CAST("'+c+'" AS BLOB))>='+str(LARGE)+' THEN NULL ELSE "'+c+'" END AS "'+c+'"' for c in selected)
        sizes = ['COALESCE(length(CAST("'+c+'" AS BLOB)),0)' for c in columns[table]]
        sql += ',(' + '+'.join(sizes) + ') AS body_size'
        rows = [dict(r) for r in db.execute('SELECT '+sql+' FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal>=? AND rollout_ordinal<? ORDER BY rollout_ordinal LIMIT 513', (sid,bucket*WIDTH,(bucket+1)*WIDTH))]
        if len(rows)>WIDTH:
            raise ValueError("Codex range has no unique ordinal index")
        pack_metadata_rows += len(rows)
        stats["metadata_rows"] += len(rows)
        return rows, body_field
    def full_row(table, meta, field):
        nonlocal used_bytes
        size = meta["body_size"] or 0
        if size > MAX_ROW or used_bytes+size > MAX_BYTES:
            return None
        sql = 'SELECT * FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal=?'
        found = db.execute(sql, (meta["thread_id"],meta["rollout_ordinal"])).fetchone()
        if found is None:
            raise ValueError("row disappeared inside read transaction")
        row = dict(found)
        stats["body_rows"] += 1
        body_threads.add(meta["thread_id"])
        stats["body_bytes"] += size
        used_bytes += size
        return row
    def capture_pack(sid, table, bucket, force=False):
        nonlocal used_packs, deferred
        marker = (sid,table,bucket)
        if marker in captured:
            return True
        if used_packs >= MAX_PACKS or pack_metadata_rows >= MAX_PACKS*WIDTH or used_bytes >= MAX_BYTES:
            return False
        metas, field = metadata(table,sid,bucket)
        visible_metas = [m for m in metas if table != "thread_turns" and m["item_type"] in VISIBLE+("",)]
        sem_key = "semantic/"+sid+"/"+table+"/"+str(bucket)
        sem_meta = stamp(visible_metas)
        sem_changed = force or not old(sem_key).get("fingerprint","").startswith("codex-sem-v1:"+sem_meta+":")
        raw_rows, messages = [], []
        staged = []
        for meta in metas:
            key = row_key(table,meta)
            meta_stamp = stamp(meta)
            large = (meta["body_size"] or 0) >= LARGE
            prior = old(key)
            skip_large = large and not force and prior.get("fingerprint","").startswith("codex-large-v1:"+meta_stamp+":")
            want_semantic = sem_changed and meta in visible_metas
            row = None
            if not skip_large or want_semantic:
                row = full_row(table,meta,field)
                if row is None:
                    deferred += 1
                    for args in staged:
                        emit(*args)
                    return False
            if large:
                if row is not None and not skip_large:
                    body = canonical({"type":"native_row","format":"codex-history","table":table,"row":row})+"\n"
                    fp = "codex-large-v1:"+meta_stamp+":"+sha(body)
                    staged.append((key,sid,"raw_row",table,body,fp,1,None))
                    ref = sha(body)
                else:
                    ref = prior["hash"]
                value = {k:v for k,v in meta.items() if k != "body_size"}
                value[field] = {"native_row_key":key,"file_hash":ref}
                raw_rows.append(value)
            else:
                raw_rows.append(row)
            if want_semantic:
                m = message(row)
                if m is not None:
                    messages.append(m)
        # Only advance a range after every row fits the bounded transaction.
        for args in staged:
            emit(*args)
        raw_key = "pack/"+sid+"/"+table+"/"+str(bucket)
        emit(raw_key,sid,"raw_pack",table,{"type":"native_pack","format":"codex-history","table":table,
             "thread_id":sid,"ordinal_start":bucket*WIDTH,"ordinal_end":(bucket+1)*WIDTH,"rows":raw_rows},rows=sum(1 for row in raw_rows if not isinstance(row.get(field),dict)),bucket=bucket)
        if table != "thread_turns" and sem_changed:
            body = "".join(canonical(m)+"\n" for m in messages)
            if body or old(sem_key):
                emit(sem_key,sid,"semantic",table,body,"codex-sem-v1:"+sem_meta+":"+sha(body),bucket=bucket)
        used_packs += 1
        captured.add(marker)
        return True
    projections = db.execute("SELECT * FROM thread_history_projection_state ORDER BY thread_id")
    for projection in projections:
        projection = dict(projection)
        sid = projection["thread_id"]
        if not isinstance(sid,str) or not sid or len(sid)>4096 or len(seen)>=MAX_THREADS:
            raise ValueError("Codex thread metadata exceeds bounded inventory")
        seen.append(sid)
        stats["metadata_rows"] += 1
        prior_state = states.get(sid)
        state = json.loads(canonical(prior_state)) if prior_state else {"v":1,"tables":{},"epoch":epoch,"reconcile_table":0,"reconcile_bucket":-1}
        captured = set()
        thread_dirty = False
        projection_stamp = stamp(projection)
        if prior_state and state.get("projection") != projection_stamp:
            thread_dirty = True
        state["projection"] = projection_stamp
        metadata_probe = None
        if metadata_db:
            wanted = ["id","title","cwd"]
            available = set(metadata_columns)
            wanted += [k for k in ("model","source","updated_at","updated_at_ms","history_mode") if k in available]
            found = db.execute("SELECT "+",".join('"'+k+'"' for k in wanted)+" FROM metadata.threads WHERE id=?",(sid,)).fetchone()
            metadata_probe = dict(found) if found else None
            stats["metadata_rows"] += 1
            meta_stamp = stamp(metadata_probe)
            if prior_state and state.get("metadata") != meta_stamp:
                thread_dirty = True
            state["metadata"] = meta_stamp
        blocked = False
        maxima = {}
        for table in TABLES:
            last = db.execute('SELECT rollout_ordinal FROM "'+table+'" WHERE thread_id=? ORDER BY rollout_ordinal DESC LIMIT 1',(sid,)).fetchone()
            maximum = last[0] if last else -1
            maxima[table] = maximum
            stats["metadata_rows"] += 1
            ts = state["tables"].setdefault(table,{"last":-1})
            if table == "thread_items" and "update" not in ts:
                latest = db.execute("SELECT updated_at_ordinal FROM thread_items WHERE thread_id=? ORDER BY updated_at_ordinal DESC LIMIT 1",(sid,)).fetchone()
                ts["update"] = [latest[0],maximum] if latest else [-1,-1]
            while ts["last"] < maximum:
                row = db.execute('SELECT rollout_ordinal FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal>? ORDER BY rollout_ordinal LIMIT 1',(sid,ts["last"])).fetchone()
                if row is None:
                    ts["last"] = maximum
                    break
                bucket = row[0]//WIDTH
                if not capture_pack(sid,table,bucket):
                    blocked = True
                    break
                ts["last"] = min(maximum,(bucket+1)*WIDTH-1)
                thread_dirty = True
            if blocked:
                break
        # Indexed item-update watermark catches revisions to earlier ranges.
        if not blocked and state["tables"].get("thread_items",{}).get("last",-1) >= maxima["thread_items"]:
            ts = state["tables"]["thread_items"]
            updates = db.execute("SELECT updated_at_ordinal,rollout_ordinal FROM thread_items WHERE thread_id=? AND (updated_at_ordinal>? OR (updated_at_ordinal=? AND rollout_ordinal>?)) ORDER BY updated_at_ordinal,rollout_ordinal LIMIT "+str(1025 if used_packs<MAX_PACKS and pack_metadata_rows<MAX_PACKS*WIDTH and used_bytes<MAX_BYTES else 1),(sid,ts["update"][0],ts["update"][0],ts["update"][1])).fetchall()
            stats["metadata_rows"] += len(updates)
            for upd in updates[:1024]:
                if not capture_pack(sid,"thread_items",upd[1]//WIDTH):
                    blocked = True
                    break
                ts["update"] = list(upd)
                thread_dirty = True
            if len(updates)>1024:
                blocked = True
        # Turns and realtime have no native update cursor. Their small metadata
        # revisions are caught by a bounded daily body reconcile, never trusted
        # as immutable merely because a turn reports completed.
        if not blocked and prior_state is None:
            state["epoch"] = epoch
            state["reconcile_table"] = len(TABLES)
        if not blocked and state.get("epoch",-1) < epoch:
            table_index = state.get("reconcile_table",0)
            if table_index >= len(TABLES):
                table_index = 0
                state["reconcile_bucket"] = -1
            while table_index < len(TABLES):
                table = TABLES[table_index]
                after = state.get("reconcile_bucket",-1)
                row = db.execute('SELECT rollout_ordinal FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal>=? ORDER BY rollout_ordinal LIMIT 1',(sid,(after+1)*WIDTH)).fetchone()
                choices = [b for b in known.get(sid,{}).get(table,[]) if b>after]
                if row is not None:
                    choices.append(row[0]//WIDTH)
                if not choices:
                    table_index += 1
                    state["reconcile_bucket"] = -1
                    state["reconcile_table"] = table_index
                    continue
                bucket = min(choices)
                if not capture_pack(sid,table,bucket,True):
                    blocked = True
                    break
                state["reconcile_bucket"] = bucket
                state["reconcile_table"] = table_index
                thread_dirty = True
            if table_index == len(TABLES):
                state["epoch"] = epoch
        if blocked:
            pending += 1
        # Thread metadata is raw custody; only the small useful context enters recall.
        # Fetch metadata only for processed/new threads or a due reconcile.
        if thread_dirty or (not blocked and prior_state is None) or (not blocked and state != prior_state):
            thread_row = None
            if metadata_db:
                size_sql = "+".join('COALESCE(length(CAST("'+c+'" AS BLOB)),0)' for c in metadata_columns)
                row_size = db.execute("SELECT "+size_sql+" FROM metadata.threads WHERE id=?",(sid,)).fetchone()
                if row_size and (row_size[0]>MAX_ROW or used_bytes+row_size[0]>MAX_BYTES):
                    deferred += 1
                    pending += 1
                    continue
                found = db.execute("SELECT * FROM metadata.threads WHERE id=?",(sid,)).fetchone()
                if found:
                    thread_row = dict(found)
                    payload_size = len(canonical(thread_row).encode())
                    if payload_size>MAX_ROW or used_bytes+payload_size>MAX_BYTES:
                        deferred += 1
                        pending += 1
                        continue
                    used_bytes += payload_size
                    stats["body_rows"] += 1
                    body_threads.add(sid)
                    stats["body_bytes"] += payload_size
            raw = {"type":"native_thread","format":"codex-history","projection_state":projection,"metadata":thread_row}
            fp = "codex-cursor-v1:"+base64.urlsafe_b64encode(canonical(state).encode()).decode().rstrip("=")
            emit("cursor/"+sid,sid,"cursor","thread_history_projection_state",raw,fp,2 if thread_row else 1)
            if thread_row:
                context = {k:thread_row[k] for k in ("title","cwd","model","source") if isinstance(thread_row.get(k),str) and thread_row[k]}
                body = canonical({"type":"native_session","format":"codex-history","thread_id":sid,"context":context})+"\n"
                emit("context/"+sid,sid,"semantic_context","threads",body)
    print(canonical({"complete":True,"pending":pending,"deferred":deferred,"seen_threads":seen,"session_bodies":len(body_threads),**stats}),flush=True)
    db.rollback()
    db.close()
main()
