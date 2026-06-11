# Schema V2 Migration Plan

`schema_v2.sql` is the complete normalized schema for a fresh database. Existing
databases need a one-time data migration because PostgreSQL cannot derive ULIDs
or reliably infer every old message sender from duplicated display names.

## Preflight

Run these checks against the legacy schema before migrating:

```sql
SELECT display_name, COUNT(*)
FROM users
GROUP BY display_name
HAVING COUNT(*) > 1;

SELECT m.user_name, COUNT(DISTINCT u.id) AS matching_users
FROM messages m
LEFT JOIN users u ON u.display_name = m.user_name
GROUP BY m.user_name
HAVING COUNT(DISTINCT u.id) <> 1;

SELECT h.user_id, h.room_name, h.role
FROM room_memberships h
LEFT JOIN signed_rooms sr ON sr.room_name = h.room_name
WHERE sr.room_name IS NULL;

SELECT entry_code, COUNT(*)
FROM signed_rooms
GROUP BY entry_code
HAVING COUNT(*) > 1;

SELECT COUNT(*) AS oversized_messages
FROM messages
WHERE char_length(message) > 2000;
```

Resolve every returned row first. Schema V2 requires unique usernames, and each
legacy message sender must map to exactly one user. Orphaned legacy memberships
need an explicit decision before the foreign-key migration:

- if an orphan has an `owned` row, create an expired V2 `signed_rooms` row for
  that room name and owner so history and revive continue to work;
- if an orphan has only `joined` rows and no owner can be identified, archive
  those rows outside the V2 relational tables or drop them intentionally.

Duplicate legacy entry codes must be regenerated before insert because V2
enforces `signed_rooms.entry_code` uniqueness.

## Migration

1. Stop writes and take a database backup.
2. Start one transaction, create a `legacy_v1` schema, and move the five
   legacy tables into it. Moving the tables also moves their indexes and avoids
   name collisions with V2 primary-key indexes.
3. Create the V2 tables and indexes from `schema_v2.sql` without its outer
   `BEGIN`/`COMMIT`.
4. In a one-off Go migration program using `github.com/oklog/ulid/v2`, generate:
   - one ULID per legacy user and store an `old_user_id -> new_user_id` map;
   - one ULID per legacy signed room and store a
     `old_room_name -> new_room_id` map;
   - one ULID per session and message.
5. Insert users, sessions, and signed rooms using those maps.
6. Insert memberships using `(room_id, user_id)` and convert roles:
   `owned -> owner`, `joined -> member`. When both old rows exist for the room
   creator, keep one `owner` row.
7. Insert messages using the room map and the resolved
   `legacy user_name -> new user_id` map.
8. Reject or truncate legacy messages whose length exceeds 2,000 characters
   before inserting into V2.
9. Verify row counts, foreign keys, and duplicate-name room behavior, then
   commit. Keep the `legacy_` tables until application verification is complete.

Expired signed-room rows should be retained so membership history and revive
continue to work. Their messages are cleared by the backend. Explicit room
deletion cascades to memberships and messages.
