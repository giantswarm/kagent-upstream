// Package lineconvert reports what the Giant Swarm line's database conversion
// (the giantswarm migration track's 000003) reads and writes: row counts and
// content digests that do not depend on the stored encoding, so a report taken
// before the conversion and one taken after it are equal when no row was lost
// or altered. A report holds counts and digests only, never stored values.
package lineconvert

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protowire"
)

// Shape names the schema a database holds.
type Shape string

const (
	// ShapeEmpty is a database without kagent's tables.
	ShapeEmpty Shape = "empty"
	// ShapeLine is a database the line's 1.x controller migrated: core 1 to 7.
	ShapeLine Shape = "line-1"
	// ShapeV1 is a database in the kagent v1.0.1 shape.
	ShapeV1 Shape = "v1"
)

// Report is what Inspect read. Tables counts the rows of every table both
// shapes share; Counts holds named counts; Digests the content digests.
type Report struct {
	Shape   Shape             `json:"shape"`
	Tables  map[string]int64  `json:"tables"`
	Counts  map[string]int64  `json:"counts"`
	Digests map[string]string `json:"digests"`
}

// Tables whose rows the conversion keeps one for one. session_push_outbox
// leaves with the conversion and is reported through Counts.
var keptTables = []string{
	"a2a_context", "agent_definition", "agent_revision", "runtime_instance", "runtime_revision",
	"sandbox", "sandbox_revision", "sandbox_template_definition", "scheduled_run", "scheduled_run_execution",
	"session", "session_checkpoint", "session_push_registration", "session_share", "session_task",
	"session_task_event", "tool", "toolserver",
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Inspect reads the tables of schema.
func Inspect(ctx context.Context, pool *pgxpool.Pool, schema string) (Report, error) {
	return inspect(ctx, pool, schema)
}

func inspect(ctx context.Context, db querier, schema string) (Report, error) {
	r := Report{Tables: map[string]int64{}, Counts: map[string]int64{}, Digests: map[string]string{}}
	in := inspector{ctx: ctx, db: db, schema: schema}
	hasSession, err := in.tableExists("session")
	if err != nil {
		return r, err
	}
	if !hasSession {
		r.Shape = ShapeEmpty
		return r, nil
	}
	line, err := in.columnExists("session_share", "permission")
	if err != nil {
		return r, err
	}
	r.Shape = ShapeV1
	if line {
		r.Shape = ShapeLine
	}
	for _, table := range keptTables {
		if r.Tables[table], err = in.count(table); err != nil {
			return r, err
		}
	}
	steps := []func(*Report) error{in.sessions, in.shares, in.snapshots, in.push, in.memory, in.deletionReasons}
	for _, step := range steps {
		if err := step(&r); err != nil {
			return r, err
		}
	}
	return r, nil
}

// Compare lists how after differs from before. A conversion from the line's
// shape to v1 must keep every table's rows and every digest.
func Compare(before, after Report) []string {
	var diffs []string
	for _, table := range keptTables {
		if before.Tables[table] != after.Tables[table] {
			diffs = append(diffs, fmt.Sprintf("table %s: %d rows before, %d after", table, before.Tables[table], after.Tables[table]))
		}
	}
	keys := make([]string, 0, len(before.Digests))
	for key := range before.Digests {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if before.Digests[key] != after.Digests[key] {
			diffs = append(diffs, "digest "+key+" differs")
		}
	}
	for _, key := range []string{"shares_writable", "push_armed", "memory_rows", "deletion_agent_deleted", "snapshots"} {
		if before.Counts[key] != after.Counts[key] {
			diffs = append(diffs, fmt.Sprintf("count %s: %d before, %d after", key, before.Counts[key], after.Counts[key]))
		}
	}
	return diffs
}

type inspector struct {
	ctx    context.Context
	db     querier
	schema string
}

func (in inspector) table(name string) string {
	return pgx.Identifier{in.schema, name}.Sanitize()
}

func (in inspector) tableExists(name string) (bool, error) {
	var exists bool
	err := in.db.QueryRow(in.ctx, "SELECT to_regclass($1) IS NOT NULL", in.table(name)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", name, err)
	}
	return exists, nil
}

func (in inspector) columnExists(table, column string) (bool, error) {
	var exists bool
	err := in.db.QueryRow(in.ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3)`, in.schema, table, column).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check column %s.%s: %w", table, column, err)
	}
	return exists, nil
}

func (in inspector) count(table string) (int64, error) {
	var n int64
	if err := in.db.QueryRow(in.ctx, "SELECT count(*) FROM "+in.table(table)).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return n, nil
}

// digest feeds each part length-prefixed, so no two part lists collide.
type digest struct{ h hash.Hash }

func newDigest() digest { return digest{h: sha256.New()} }

func (d digest) add(parts ...[]byte) {
	var n [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		d.h.Write(n[:])
		d.h.Write(part)
	}
}

func (d digest) sum() string { return hex.EncodeToString(d.h.Sum(nil)) }

type field struct {
	num     protowire.Number
	typ     protowire.Type
	raw     []byte
	payload []byte
}

func fields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		m := protowire.ConsumeFieldValue(num, typ, b[n:])
		if m < 0 {
			return nil, protowire.ParseError(m)
		}
		f := field{num: num, typ: typ, raw: b[:n+m]}
		if typ == protowire.BytesType {
			f.payload, _ = protowire.ConsumeBytes(b[n:])
		}
		out = append(out, f)
		b = b[n+m:]
	}
	return out, nil
}

func stringField(b []byte, num protowire.Number) ([]byte, error) {
	fs, err := fields(b)
	if err != nil {
		return nil, err
	}
	var value []byte
	for _, f := range fs {
		if f.num == num && f.typ == protowire.BytesType {
			value = f.payload
		}
	}
	return value, nil
}

// sessions digests every Session by its id, the Agent's namespace and name and
// the bytes of every field but 15, in order. Field 15 is ResourceReference in
// the line's shape and AgentProvenance{ref} in v1.
func (in inspector) sessions(r *Report) error {
	d := newDigest()
	var undecodable int64
	rows, err := in.db.Query(in.ctx, "SELECT id::text, data FROM "+in.table("session")+" ORDER BY id")
	if err != nil {
		return fmt.Errorf("read sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return fmt.Errorf("read sessions: %w", err)
		}
		fs, err := fields(data)
		if err != nil {
			undecodable++
			continue
		}
		var namespace, name, rest []byte
		for _, f := range fs {
			if f.num != 15 || f.typ != protowire.BytesType {
				rest = append(rest, f.raw...)
				continue
			}
			ref := f.payload
			if r.Shape == ShapeV1 {
				if ref, err = stringField(f.payload, 1); err != nil {
					undecodable++
					continue
				}
			}
			if namespace, err = stringField(ref, 1); err != nil {
				undecodable++
				continue
			}
			if name, err = stringField(ref, 2); err != nil {
				undecodable++
				continue
			}
		}
		d.add([]byte(id), namespace, name, rest)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read sessions: %w", err)
	}
	r.Digests["sessions"] = d.sum()
	r.Counts["sessions_undecodable"] = undecodable
	return nil
}

// shares digests every share by its columns, its writable scope and its
// stored timestamps, whose field numbers the two shapes differ in.
func (in inspector) shares(r *Report) error {
	query := `SELECT s.id::text, s.session_id::text, s.token_hash, COALESCE(s.expires_at::text, ''), s.data, `
	if r.Shape == ShapeLine {
		query += `s.permission = 'SESSION_SHARE_PERMISSION_READ_WRITE' FROM ` + in.table("session_share") + ` s ORDER BY s.id`
	} else {
		query += `COALESCE(c.writable, false) FROM ` + in.table("session_share") + ` s LEFT JOIN ` +
			in.table("session_share_scope") + ` c ON c.share_id = s.id ORDER BY s.id`
	}
	rows, err := in.db.Query(in.ctx, query)
	if err != nil {
		return fmt.Errorf("read shares: %w", err)
	}
	defer rows.Close()
	created, expires := protowire.Number(3), protowire.Number(4)
	if r.Shape == ShapeLine {
		created, expires = 4, 5
	}
	d := newDigest()
	var writable int64
	for rows.Next() {
		var id, sessionID, expiresAt string
		var tokenHash, data []byte
		var canWrite bool
		if err := rows.Scan(&id, &sessionID, &tokenHash, &expiresAt, &data, &canWrite); err != nil {
			return fmt.Errorf("read shares: %w", err)
		}
		fs, err := fields(data)
		if err != nil {
			return fmt.Errorf("decode share %s: %w", id, err)
		}
		var createdAt, expiresField, rest []byte
		for _, f := range fs {
			switch {
			case f.num == created && f.typ == protowire.BytesType:
				createdAt = f.payload
			case f.num == expires && f.typ == protowire.BytesType:
				expiresField = f.payload
			case r.Shape == ShapeLine && f.num == 3 && f.typ == protowire.VarintType:
			default:
				rest = append(rest, f.raw...)
			}
		}
		flag := []byte{0}
		if canWrite {
			flag[0] = 1
			writable++
		}
		d.add([]byte(id), []byte(sessionID), tokenHash, []byte(expiresAt), flag, createdAt, expiresField, rest)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read shares: %w", err)
	}
	r.Digests["shares"] = d.sum()
	r.Counts["shares_writable"] = writable
	return nil
}

// snapshots digests the snapshot references of checkpoints, tasks and task
// events; their scope or fidelity is counted per value.
func (in inspector) snapshots(r *Report) error {
	column := "snapshot_fidelity"
	if r.Shape == ShapeLine {
		column = "snapshot_content_scope"
	}
	queries := map[string]string{
		"session_checkpoint": "SELECT id::text, snapshot_uri, " + column + " FROM " + in.table("session_checkpoint") + " ORDER BY id",
		"session_task":       "SELECT history_id::text || '/' || id, snapshot_uri, " + column + " FROM " + in.table("session_task") + " WHERE snapshot_uri IS NOT NULL ORDER BY history_id, id",
		"session_task_event": "SELECT sequence::text, snapshot_uri, " + column + " FROM " + in.table("session_task_event") + " WHERE snapshot_uri IS NOT NULL ORDER BY sequence",
	}
	var total int64
	for _, table := range []string{"session_checkpoint", "session_task", "session_task_event"} {
		rows, err := in.db.Query(in.ctx, queries[table])
		if err != nil {
			return fmt.Errorf("read %s snapshots: %w", table, err)
		}
		d := newDigest()
		for rows.Next() {
			var key, uri, scope string
			if err := rows.Scan(&key, &uri, &scope); err != nil {
				rows.Close()
				return fmt.Errorf("read %s snapshots: %w", table, err)
			}
			d.add([]byte(key), []byte(uri))
			r.Counts["snapshot_"+strings.ToLower(scope)]++
			total++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read %s snapshots: %w", table, err)
		}
		r.Digests[table+"_snapshots"] = d.sum()
	}
	r.Counts["snapshots"] = total
	return nil
}

// push digests the delivery each registration owes: in the line's shape the
// newest undelivered outbox event of an open registration bound to a task, in
// v1 its pending event.
func (in inspector) push(r *Report) error {
	var query string
	if r.Shape == ShapeLine {
		query = `SELECT p.id::text, max(o.source_event_sequence) FROM ` + in.table("session_push_registration") + ` p
			JOIN ` + in.table("session_push_outbox") + ` o ON o.registration_id = p.id
			WHERE o.state IN ('pending', 'sending') AND p.closed_at IS NULL AND p.task_id IS NOT NULL
			GROUP BY p.id ORDER BY p.id`
		var undeliverable, outbox int64
		if err := in.db.QueryRow(in.ctx, `SELECT count(*) FILTER (WHERE o.state IN ('pending', 'sending')
				AND (p.closed_at IS NOT NULL OR p.task_id IS NULL)), count(*)
			FROM `+in.table("session_push_outbox")+` o JOIN `+in.table("session_push_registration")+` p ON p.id = o.registration_id`).
			Scan(&undeliverable, &outbox); err != nil {
			return fmt.Errorf("count push outbox: %w", err)
		}
		r.Counts["push_outbox_rows"] = outbox
		r.Counts["push_undelivered_closed"] = undeliverable
	} else {
		query = `SELECT id::text, pending_event_sequence FROM ` + in.table("session_push_registration") + `
			WHERE pending_event_sequence IS NOT NULL ORDER BY id`
	}
	rows, err := in.db.Query(in.ctx, query)
	if err != nil {
		return fmt.Errorf("read push deliveries: %w", err)
	}
	defer rows.Close()
	d := newDigest()
	var armed int64
	for rows.Next() {
		var id string
		var sequence int64
		if err := rows.Scan(&id, &sequence); err != nil {
			return fmt.Errorf("read push deliveries: %w", err)
		}
		d.add([]byte(id), []byte(fmt.Sprint(sequence)))
		armed++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read push deliveries: %w", err)
	}
	r.Digests["push_deliveries"] = d.sum()
	r.Counts["push_armed"] = armed
	return nil
}

// memory digests long-term memory rows by the key v1 reads: the line's
// <namespace>__NS__<name> spelling, and a card name the Agent's namespace
// resolves, are read as <namespace>/<name>.
func (in inspector) memory(r *Report) error {
	exists, err := in.tableExists("memory")
	if err != nil || !exists {
		return err
	}
	namespaces := map[string][]string{}
	rows, err := in.db.Query(in.ctx, "SELECT DISTINCT agent_name, namespace FROM "+in.table("agent_definition"))
	if err != nil {
		return fmt.Errorf("read agent definitions: %w", err)
	}
	for rows.Next() {
		var name, namespace string
		if err := rows.Scan(&name, &namespace); err != nil {
			rows.Close()
			return fmt.Errorf("read agent definitions: %w", err)
		}
		namespaces[name] = append(namespaces[name], namespace)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read agent definitions: %w", err)
	}
	rows, err = in.db.Query(in.ctx, `SELECT id, COALESCE(agent_name, ''), COALESCE(user_id, ''),
		md5(COALESCE(content, '')), md5(COALESCE(metadata, '')) FROM `+in.table("memory")+` ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read memory: %w", err)
	}
	defer rows.Close()
	d := newDigest()
	var total, unresolved, ambiguous int64
	for rows.Next() {
		var id, key, user, content, metadata string
		if err := rows.Scan(&id, &key, &user, &content, &metadata); err != nil {
			return fmt.Errorf("read memory: %w", err)
		}
		total++
		if r.Shape == ShapeLine {
			var state int
			key, state = MemoryKey(key, namespaces)
			switch state {
			case keyUnresolved:
				unresolved++
			case keyAmbiguous:
				ambiguous++
			}
		}
		d.add([]byte(id), []byte(key), []byte(user), []byte(content), []byte(metadata))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read memory: %w", err)
	}
	r.Digests["memory"] = d.sum()
	r.Counts["memory_rows"] = total
	r.Counts["memory_unresolved"] = unresolved
	r.Counts["memory_ambiguous"] = ambiguous
	return nil
}

const (
	keyResolved = iota
	keyUnresolved
	keyAmbiguous
)

// MemoryKey returns the v1 key of a memory row the line's runtime wrote, and
// whether a key without a namespace resolved. namespaces maps an Agent's name
// to the namespaces that hold an Agent of that name.
func MemoryKey(key string, namespaces map[string][]string) (string, int) {
	if strings.Contains(key, "/") {
		return key, keyResolved
	}
	if namespace, name, ok := strings.Cut(key, "__NS__"); ok {
		return strings.ReplaceAll(namespace, "_", "-") + "/" + strings.ReplaceAll(name, "_", "-"), keyResolved
	}
	name := strings.ReplaceAll(key, "_", "-")
	switch held := slices.Compact(slices.Sorted(slices.Values(namespaces[name]))); len(held) {
	case 0:
		return key, keyUnresolved
	case 1:
		return held[0] + "/" + name, keyResolved
	default:
		return key, keyAmbiguous
	}
}

func (in inspector) deletionReasons(r *Report) error {
	var n int64
	if err := in.db.QueryRow(in.ctx, "SELECT count(*) FROM "+in.table("session")+" WHERE deletion_reason = 'agent_deleted'").Scan(&n); err != nil {
		return fmt.Errorf("count deletion reasons: %w", err)
	}
	r.Counts["deletion_agent_deleted"] = n
	return nil
}
