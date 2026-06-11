BEGIN;

CREATE TABLE users (
	id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
	email TEXT NOT NULL UNIQUE,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_sessions (
	id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE,
	expires_at TIMESTAMPTZ NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE signed_rooms (
	id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
	room_name TEXT NOT NULL,
	owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	entry_code TEXT NOT NULL UNIQUE CHECK (entry_code ~ '^[0-9]{4}$'),
	expires_at TIMESTAMPTZ NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE room_memberships (
	room_id TEXT NOT NULL REFERENCES signed_rooms(id) ON DELETE CASCADE,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
	joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	last_visited_at TIMESTAMPTZ,
	PRIMARY KEY (room_id, user_id)
);

CREATE TABLE messages (
	id TEXT PRIMARY KEY CHECK (char_length(id) = 26),
	room_id TEXT NOT NULL REFERENCES signed_rooms(id) ON DELETE CASCADE,
	sender_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	message TEXT NOT NULL CHECK (char_length(message) <= 2000),
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_token_hash ON user_sessions(token_hash);
CREATE INDEX idx_user_sessions_expires_at ON user_sessions(expires_at);
CREATE INDEX idx_signed_rooms_owner_user_id ON signed_rooms(owner_user_id);
CREATE INDEX idx_signed_rooms_expires_at ON signed_rooms(expires_at);
CREATE INDEX idx_room_memberships_user_id ON room_memberships(user_id);
CREATE INDEX idx_room_memberships_room_id ON room_memberships(room_id);
CREATE INDEX idx_messages_room_id ON messages(room_id);
CREATE INDEX idx_messages_sender_user_id ON messages(sender_user_id);

COMMIT;
