-- 0010_quizly_attempts_social: battles, attempts, answers, follows, leaderboard.
-- FKs to users are UUID. The leaderboard view maps users.name -> display_name.

-- 9. BATTLES (multiplayer room; defined before attempts which reference it)
CREATE TABLE battles (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  room_code    CHAR(6) NOT NULL CHECK (room_code ~ '^[A-Z0-9]{6}$'),
  host_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  test_id      BIGINT NOT NULL REFERENCES tests(id) ON DELETE RESTRICT,
  status       VARCHAR(10) NOT NULL DEFAULT 'lobby'
                 CHECK (status IN ('lobby', 'active', 'finished', 'abandoned')),
  max_players  SMALLINT NOT NULL DEFAULT 8 CHECK (max_players BETWEEN 2 AND 50),
  started_at   TIMESTAMPTZ,
  finished_at  TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- room_code unique only among joinable/live rooms, so finished rooms free it up.
CREATE UNIQUE INDEX uq_battles_live_code ON battles (room_code)
  WHERE status IN ('lobby', 'active');
CREATE INDEX idx_battles_host   ON battles (host_id);
CREATE INDEX idx_battles_status ON battles (status);
CREATE TRIGGER trg_battles_updated BEFORE UPDATE ON battles
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 10. TEST_ATTEMPTS (a user's run of a test; solo or inside a battle)
CREATE TABLE test_attempts (
  id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id          UUID   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  test_id          BIGINT NOT NULL REFERENCES tests(id) ON DELETE RESTRICT,
  battle_id        BIGINT REFERENCES battles(id) ON DELETE SET NULL,  -- NULL = solo
  language_code    CHAR(2) NOT NULL CHECK (language_code IN ('en', 'bn')),
  score            SMALLINT NOT NULL DEFAULT 0 CHECK (score >= 0),
  total_questions  SMALLINT NOT NULL CHECK (total_questions > 0),
  accuracy         NUMERIC(5,2) GENERATED ALWAYS AS
                     (round((score::numeric / total_questions) * 100, 2)) STORED,
  points_earned    INT NOT NULL DEFAULT 0,
  started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at     TIMESTAMPTZ,
  CONSTRAINT chk_score_within_total CHECK (score <= total_questions)
);
CREATE INDEX idx_attempts_user_completed ON test_attempts (user_id, completed_at DESC);
CREATE INDEX idx_attempts_test           ON test_attempts (test_id);
CREATE INDEX idx_attempts_battle         ON test_attempts (battle_id);
CREATE INDEX idx_attempts_completed_at   ON test_attempts (completed_at)
  WHERE completed_at IS NOT NULL;

-- 11. ATTEMPT_ANSWERS (per-question response; keeps its own is_correct for history)
CREATE TABLE attempt_answers (
  id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  attempt_id          BIGINT NOT NULL REFERENCES test_attempts(id)   ON DELETE CASCADE,
  question_id         BIGINT NOT NULL REFERENCES questions(id)       ON DELETE RESTRICT,
  selected_option_id  BIGINT REFERENCES question_options(id) ON DELETE SET NULL,  -- NULL = skipped
  is_correct          BOOLEAN NOT NULL,
  answered_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (attempt_id, question_id)
);
CREATE INDEX idx_answers_attempt  ON attempt_answers (attempt_id);
CREATE INDEX idx_answers_question ON attempt_answers (question_id);

-- 12. BATTLE_PARTICIPANTS (who is in a battle + their per-battle result)
CREATE TABLE battle_participants (
  battle_id    BIGINT NOT NULL REFERENCES battles(id) ON DELETE CASCADE,
  user_id      UUID   NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  role         VARCHAR(8) NOT NULL DEFAULT 'player' CHECK (role IN ('host', 'player')),
  attempt_id   BIGINT REFERENCES test_attempts(id) ON DELETE SET NULL,
  score        SMALLINT CHECK (score IS NULL OR score >= 0),
  placement    SMALLINT CHECK (placement IS NULL OR placement > 0),
  joined_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at  TIMESTAMPTZ,
  PRIMARY KEY (battle_id, user_id)
);
CREATE INDEX idx_participants_user ON battle_participants (user_id);

-- 13. USER_FOLLOWS (Friends / following — directed many-to-many on users)
CREATE TABLE user_follows (
  follower_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  followee_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (follower_id, followee_id),
  CONSTRAINT chk_no_self_follow CHECK (follower_id <> followee_id)
);
CREATE INDEX idx_follows_followee ON user_follows (followee_id);

-- 14. Leaderboard view (maps this app's users.name -> display_name).
CREATE VIEW v_global_leaderboard AS
  SELECT id AS user_id, name AS display_name, handle, total_points,
         RANK() OVER (ORDER BY total_points DESC) AS rank
  FROM users;
