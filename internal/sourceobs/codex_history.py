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
    capacity_waiting = None
    fair_phase = False
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
        nonlocal used_bytes, capacity_waiting
        size = meta["body_size"] or 0
        if size > MAX_ROW:
            return None
        if used_bytes+size > byte_limit:
            if fair_phase and capacity_waiting is None:
                capacity_waiting = meta["thread_id"]
            return None
        keys = ["thread_id","turn_id"] if table=="thread_turns" else (["thread_id","turn_id","item_id"] if table=="thread_items" else ["thread_id","item_id"])
        sql = 'SELECT * FROM "'+table+'" WHERE '+" AND ".join('"'+key+'"=?' for key in keys)
        found = db.execute(sql,tuple(meta[key] for key in keys)).fetchone()
        if found is None:
            raise ValueError("row disappeared inside read transaction")
        row = dict(found)
        stats["body_rows"] += 1
        body_threads.add(meta["thread_id"])
        stats["body_bytes"] += size
        used_bytes += size
        return row
    def capture_pack(sid, table, bucket, force=False):
        nonlocal used_packs, deferred, capacity_waiting
        force = force or states.get(sid,{}).get("epoch",epoch)<epoch
        marker = (sid,table,bucket)
        if marker in captured:
            return True
        if used_packs >= MAX_PACKS or pack_metadata_rows >= MAX_PACKS*WIDTH or used_bytes >= MAX_BYTES:
            if fair_phase and capacity_waiting is None:
                capacity_waiting = sid
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
    # Inventory/cursors are the existing scheduler checkpoint. No second queue.
    captured = set()
    infos = []
    for projection in db.execute("SELECT * FROM thread_history_projection_state ORDER BY thread_id"):
        projection = dict(projection)
        sid = projection["thread_id"]
        if not isinstance(sid,str) or not sid or len(sid)>4096 or len(seen)>=MAX_THREADS:
            raise ValueError("Codex thread metadata exceeds bounded inventory")
        seen.append(sid)
        stats["metadata_rows"] += 1
        prior = states.get(sid)
        state = json.loads(canonical(prior)) if prior else {"v":1,"tables":{},"epoch":epoch,"reconcile_table":len(TABLES),"reconcile_bucket":-1}
        state["projection"] = stamp(projection)
        probe = None
        if metadata_db:
            wanted = ["id","title","cwd"]
            wanted += [k for k in ("model","source","updated_at","updated_at_ms","history_mode") if k in metadata_columns]
            selected = ['CASE WHEN length(CAST("'+k+'" AS BLOB))<'+str(LARGE)+' THEN "'+k+'" ELSE NULL END AS "'+k+'"' for k in wanted]
            found = db.execute("SELECT "+",".join(selected)+" FROM metadata.threads WHERE id=?",(sid,)).fetchone()
            probe = dict(found) if found else None
            stats["metadata_rows"] += 1
            state["metadata"] = stamp(probe)
        maxima = {}
        latest_update = [-1,-1]
        recency = 0
        for table in TABLES:
            fields = "rollout_ordinal" if table=="thread_turns" else "rollout_ordinal,created_at_ms"
            row = db.execute('SELECT '+fields+' FROM "'+table+'" WHERE thread_id=? ORDER BY rollout_ordinal DESC LIMIT 1',(sid,)).fetchone()
            maxima[table] = row[0] if row else -1
            if row and len(row)>1:
                recency = max(recency,row[1] or 0)
            stats["metadata_rows"] += 1
            state["tables"].setdefault(table,{"last":-1})
        upd = db.execute("SELECT updated_at_ordinal,rollout_ordinal FROM thread_items WHERE thread_id=? ORDER BY updated_at_ordinal DESC,rollout_ordinal DESC LIMIT 1",(sid,)).fetchone()
        stats["metadata_rows"] += 1
        if upd:
            latest_update = list(upd)
        state["tables"]["thread_items"].setdefault("update",latest_update)
        # Recent visible turn anchors can precede a long tail of raw tool output.
        targets = []
        if {"first_user_item_id","final_agent_item_id"} <= set(columns["thread_turns"]):
            turn = db.execute("SELECT turn_id,first_user_item_id,final_agent_item_id FROM thread_turns WHERE thread_id=? ORDER BY rollout_ordinal DESC LIMIT 1",(sid,)).fetchone()
            stats["metadata_rows"] += 1
            if turn:
                for item_id in (turn[1],turn[2]):
                    if not item_id:
                        continue
                    row = db.execute("SELECT rollout_ordinal FROM thread_items WHERE thread_id=? AND turn_id=? AND item_id=?",(sid,turn[0],item_id)).fetchone()
                    stats["metadata_rows"] += 1
                    target = ["thread_items",row[0]//WIDTH] if row else None
                    if target and target not in targets:
                        targets.append(target)
        for table in TABLES:
            target = [table,maxima[table]//WIDTH]
            if maxima[table]>=0 and target not in targets:
                targets.append(target)
        revision = stamp([projection,maxima,latest_update])
        if state.get("tail_revision") != revision:
            state["tail_revision"] = revision
            state["tail_pending"] = targets
        infos.append({"sid":sid,"projection":projection,"probe":probe,"prior":prior,
                      "state":state,"maxima":maxima,"update":latest_update,"recency":recency})
    def contiguous(info, table, bucket):
        ts = info["state"]["tables"][table]
        row = db.execute('SELECT rollout_ordinal FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal>? ORDER BY rollout_ordinal LIMIT 1',(info["sid"],ts["last"])).fetchone()
        stats["metadata_rows"] += 1
        # A tail never advances the contiguous history over an unowned range.
        if row is not None and row[0]//WIDTH == bucket:
            ts["last"] = min(info["maxima"][table],(bucket+1)*WIDTH-1)
    def pack(info, table, bucket, force=False):
        if not capture_pack(info["sid"],table,bucket,force):
            return False
        contiguous(info,table,bucket)
        return True
    def tail(info):
        state = info["state"]
        todo = state.get("tail_pending",[])
        if not todo:
            return False
        table,bucket = todo[0]
        if not pack(info,table,bucket):
            return False
        state["tail_pending"] = todo[1:]
        return True
    def backfill(info):
        state = info["state"]
        start = state.get("backfill_table",0)
        for offset in range(len(TABLES)):
            index = (start+offset)%len(TABLES)
            table = TABLES[index]
            ts = state["tables"][table]
            if ts["last"] >= info["maxima"][table]:
                continue
            row = db.execute('SELECT rollout_ordinal FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal>? ORDER BY rollout_ordinal LIMIT 1',(info["sid"],ts["last"])).fetchone()
            stats["metadata_rows"] += 1
            if row is None:
                continue
            state["backfill_table"] = (index+1)%len(TABLES)
            return pack(info,table,row[0]//WIDTH)
        return False
    def updates(info):
        ts = info["state"]["tables"]["thread_items"]
        if ts["update"] >= info["update"]:
            return False
        rows = db.execute("SELECT updated_at_ordinal,rollout_ordinal FROM thread_items WHERE thread_id=? AND (updated_at_ordinal>? OR (updated_at_ordinal=? AND rollout_ordinal>?)) ORDER BY updated_at_ordinal,rollout_ordinal LIMIT 1025",(info["sid"],ts["update"][0],ts["update"][0],ts["update"][1])).fetchall()
        stats["metadata_rows"] += len(rows)
        if not rows:
            return False
        bucket = rows[0][1]//WIDTH
        if not pack(info,"thread_items",bucket):
            return False
        for row in rows:
            if row[1]//WIDTH != bucket:
                break
            ts["update"] = list(row)
        return True
    def reconcile(info):
        state = info["state"]
        if state.get("epoch",-1)>=epoch:
            return False
        index = state.get("reconcile_table",0)
        if index >= len(TABLES):
            index = 0
            state["reconcile_bucket"] = -1
        while index < len(TABLES):
            table = TABLES[index]
            after = state.get("reconcile_bucket",-1)
            row = db.execute('SELECT rollout_ordinal FROM "'+table+'" WHERE thread_id=? AND rollout_ordinal>=? ORDER BY rollout_ordinal LIMIT 1',(info["sid"],(after+1)*WIDTH)).fetchone()
            stats["metadata_rows"] += 1
            choices = [b for b in known.get(info["sid"],{}).get(table,[]) if b>after]
            if row is not None:
                choices.append(row[0]//WIDTH)
            if choices:
                bucket = min(choices)
                if not pack(info,table,bucket,True):
                    return False
                state["reconcile_bucket"] = bucket
                state["reconcile_table"] = index
                return True
            index += 1
            state["reconcile_bucket"] = -1
            state["reconcile_table"] = index
        state["epoch"] = epoch
        return True
    def metadata_due(info):
        state = info["state"]
        return metadata_db and (state.get("metadata_owned")!=state.get("metadata") or state.get("metadata_epoch",-1)<epoch)
    def own_metadata(info):
        nonlocal used_bytes,deferred,capacity_waiting
        if not metadata_due(info):
            return False
        sid,state = info["sid"],info["state"]
        size_sql = "+".join('COALESCE(length(CAST("'+c+'" AS BLOB)),0)' for c in metadata_columns)
        size = db.execute("SELECT "+size_sql+" FROM metadata.threads WHERE id=?",(sid,)).fetchone()
        stats["metadata_rows"] += 1
        if size and (size[0]>MAX_ROW or used_bytes+size[0]>byte_limit):
            deferred += 1
            if size[0]<=MAX_ROW and fair_phase and capacity_waiting is None:
                capacity_waiting = sid
            return False
        found = db.execute("SELECT * FROM metadata.threads WHERE id=?",(sid,)).fetchone()
        row = dict(found) if found else None
        payload_size = size[0] if size else 0
        used_bytes += payload_size
        stats["body_rows"] += 1 if row else 0
        stats["body_bytes"] += payload_size
        if row:
            body_threads.add(sid)
        emit("metadata/"+sid,sid,"raw_row","threads",{"type":"native_row","format":"codex-history","table":"threads","row":row},rows=1 if row else 0)
        state["metadata_owned"] = state.get("metadata")
        state["metadata_epoch"] = epoch
        return True
    # Reserve at least 24 range/96 MiB slots for rotating work. Recent tails
    # cannot consume the history portion, and cold history cannot hide tails.
    byte_limit = 32 << 20
    for info in sorted((x for x in infos if x["state"].get("tail_pending")),key=lambda x:(-x["recency"],x["sid"]))[:8]:
        tail(info)
    byte_limit = MAX_BYTES
    fair_phase = True
    next_thread = req.get("next_thread","")
    start = next((i for i,info in enumerate(infos) if info["sid"]>=next_thread),0)
    ordered = infos[start:]+infos[:start]
    actions = (backfill,updates,reconcile,tail,own_metadata)
    # Each visit completes at most one action. Class rotation protects history
    # against a continuously growing tail and updates against a long backfill.
    for _ in range(MAX_PACKS*len(actions)+len(actions)):
        progress = False
        for index,info in enumerate(ordered):
            if used_packs>=MAX_PACKS or pack_metadata_rows>=MAX_PACKS*WIDTH or used_bytes>=MAX_BYTES:
                if capacity_waiting is None:
                    capacity_waiting = info["sid"]
                break
            state = info["state"]
            action_start = state.get("action",0)
            for offset in range(len(actions)):
                action_index = (action_start+offset)%len(actions)
                if actions[action_index](info):
                    state["action"] = (action_index+1)%len(actions)
                    next_thread = ordered[(index+1)%len(ordered)]["sid"]
                    progress = True
                    break
        if not progress or used_packs>=MAX_PACKS or pack_metadata_rows>=MAX_PACKS*WIDTH or used_bytes>=MAX_BYTES:
            break
    next_thread = capacity_waiting or next_thread
    for info in infos:
        sid,state,prior = info["sid"],info["state"],info["prior"]
        unfinished = (any(state["tables"][table]["last"]<info["maxima"][table] for table in TABLES)
                      or state["tables"]["thread_items"]["update"]<info["update"]
                      or state.get("epoch",-1)<epoch or bool(state.get("tail_pending"))
                      or metadata_due(info))
        if unfinished:
            pending += 1
        # The small progress cursor must publish even when a metadata body is
        # too large/deferred. Its source ref points at separately owned metadata.
        if state!=prior:
            raw = {"type":"native_thread","format":"codex-history","projection_state":info["projection"],
                   "metadata_key":"metadata/"+sid if metadata_db else None}
            fp = "codex-cursor-v1:"+base64.urlsafe_b64encode(canonical(state).encode()).decode().rstrip("=")
            emit("cursor/"+sid,sid,"cursor","thread_history_projection_state",raw,fp,1)
        probe = info["probe"]
        context_key = "context/"+sid
        if probe and (prior is None or prior.get("metadata")!=state.get("metadata")):
            has_items = info["maxima"]["thread_items"]>=0 or info["maxima"]["thread_realtime_items"]>=0
            if has_items:
                context = {k:probe[k] for k in ("title","cwd","model","source") if isinstance(probe.get(k),str) and probe[k]}
                body = canonical({"type":"native_session","format":"codex-history","thread_id":sid,"context":context})+"\n"
                emit(context_key,sid,"semantic_context","threads",body)
            elif old(context_key):
                emit(context_key,sid,"cursor","threads",{"type":"native_context_without_items","thread_id":sid})
    print(canonical({"complete":True,"pending":pending,"deferred":deferred,"seen_threads":seen,"next_thread":next_thread,"session_bodies":len(body_threads),**stats}),flush=True)
    db.rollback()
    db.close()

if __name__=="__main__":
    try:
        main()
    except Exception as exc:
        detail=str(exc)
        safe=("unsupported Codex history schema:","native item_json must be an object","Codex range has no unique ordinal index","Codex thread metadata exceeds bounded inventory","row disappeared inside read transaction")
        if isinstance(exc,ValueError) and detail.startswith(safe):
            print(detail,file=sys.stderr)
        else:
            print("Codex history read failed ("+type(exc).__name__+"); check native row encoding/schema",file=sys.stderr)
        sys.exit(1)
