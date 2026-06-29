-- 0009_quizly_content: content hierarchy and question bank.
-- subjects -> chapters -> {chapter_notes, questions -> question_options}, tests.
-- set_updated_at() already exists (migration 0002). FKs to users are UUID.

-- 1. SUBJECTS (top of the content hierarchy; manually ordered)
CREATE TABLE subjects (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name_en     VARCHAR(120) NOT NULL,
  name_bn     VARCHAR(120) NOT NULL,
  position    INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_subjects_name_en UNIQUE (name_en),
  CONSTRAINT uq_subjects_order   UNIQUE (position) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER trg_subjects_updated BEFORE UPDATE ON subjects
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 2. CHAPTERS (belong to a subject; ordered within the subject)
CREATE TABLE chapters (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_id  BIGINT NOT NULL REFERENCES subjects(id) ON DELETE RESTRICT,
  name_en     VARCHAR(160) NOT NULL,
  name_bn     VARCHAR(160) NOT NULL,
  position    INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_chapter_name  UNIQUE (subject_id, name_en),
  CONSTRAINT uq_chapter_order UNIQUE (subject_id, position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX idx_chapters_subject ON chapters (subject_id);
CREATE TRIGGER trg_chapters_updated BEFORE UPDATE ON chapters
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 3. CHAPTER_NOTES (PDF study material; one row per language; PDF in object storage)
CREATE TABLE chapter_notes (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  chapter_id     BIGINT NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
  language_code  CHAR(2) NOT NULL CHECK (language_code IN ('en', 'bn')),
  title          VARCHAR(200) NOT NULL,
  pdf_url        TEXT NOT NULL,
  page_count     INT CHECK (page_count IS NULL OR page_count > 0),
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (chapter_id, language_code)
);
CREATE INDEX idx_chapter_notes_chapter ON chapter_notes (chapter_id);
CREATE TRIGGER trg_chapter_notes_updated BEFORE UPDATE ON chapter_notes
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 4. QUESTIONS (question bank; lives in a chapter; bilingual Markdown)
CREATE TABLE questions (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  chapter_id      BIGINT NOT NULL REFERENCES chapters(id) ON DELETE RESTRICT,
  prompt_en       TEXT NOT NULL,
  prompt_bn       TEXT NOT NULL,
  explanation_en  TEXT,
  explanation_bn  TEXT,
  position        INT NOT NULL DEFAULT 0,
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_question_order UNIQUE (chapter_id, position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX idx_questions_chapter ON questions (chapter_id);
CREATE INDEX idx_questions_author  ON questions (created_by);
CREATE TRIGGER trg_questions_updated BEFORE UPDATE ON questions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 5. QUESTION_OPTIONS (MCQ choices; bilingual; is_correct is SERVER-ONLY)
CREATE TABLE question_options (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  question_id  BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  position     SMALLINT NOT NULL,
  body_en      TEXT NOT NULL,
  body_bn      TEXT NOT NULL,
  is_correct   BOOLEAN NOT NULL DEFAULT false,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_option_order UNIQUE (question_id, position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX idx_options_question ON question_options (question_id);
CREATE UNIQUE INDEX uq_one_correct_per_question
  ON question_options (question_id) WHERE is_correct;
CREATE TRIGGER trg_options_updated BEFORE UPDATE ON question_options
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Row-count rules SQL can't express as a plain constraint: at COMMIT every
-- question must have >= 2 options and EXACTLY ONE correct.
CREATE OR REPLACE FUNCTION validate_question_options()
RETURNS TRIGGER AS $$
DECLARE
  qid           BIGINT := COALESCE(NEW.question_id, OLD.question_id);
  opt_count     INT;
  correct_count INT;
BEGIN
  SELECT count(*), count(*) FILTER (WHERE is_correct)
    INTO opt_count, correct_count
  FROM question_options WHERE question_id = qid;

  IF opt_count = 0 THEN
    RETURN NULL;
  END IF;
  IF opt_count < 2 THEN
    RAISE EXCEPTION 'Question % must have at least 2 options (has %)', qid, opt_count;
  END IF;
  IF correct_count <> 1 THEN
    RAISE EXCEPTION 'Question % must have exactly 1 correct option (has %)', qid, correct_count;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER trg_validate_question_options
  AFTER INSERT OR UPDATE OR DELETE ON question_options
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION validate_question_options();

-- 6. TESTS (chapter / multi-chapter / full-subject; manually ordered)
CREATE TABLE tests (
  id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  subject_id    BIGINT NOT NULL REFERENCES subjects(id) ON DELETE RESTRICT,
  scope_type    VARCHAR(13) NOT NULL
                  CHECK (scope_type IN ('chapter', 'multi_chapter', 'subject')),
  title_en      VARCHAR(200) NOT NULL,
  title_bn      VARCHAR(200) NOT NULL,
  test_code     VARCHAR(16) NOT NULL,
  difficulty    VARCHAR(10) CHECK (difficulty IN ('easy', 'medium', 'hard')),
  position      INT NOT NULL DEFAULT 0,
  is_published  BOOLEAN NOT NULL DEFAULT false,
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_test_code  UNIQUE (test_code),
  CONSTRAINT uq_test_order UNIQUE (subject_id, position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX idx_tests_subject ON tests (subject_id);
CREATE INDEX idx_tests_browse  ON tests (subject_id, is_published, position);
CREATE INDEX idx_tests_author  ON tests (created_by);
CREATE TRIGGER trg_tests_updated BEFORE UPDATE ON tests
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 7. TEST_CHAPTERS (which chapters a test covers — junction)
CREATE TABLE test_chapters (
  test_id     BIGINT NOT NULL REFERENCES tests(id)    ON DELETE CASCADE,
  chapter_id  BIGINT NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
  PRIMARY KEY (test_id, chapter_id)
);
CREATE INDEX idx_test_chapters_chapter ON test_chapters (chapter_id);

-- 8. TEST_QUESTIONS (which questions a test pulls + the order students see)
CREATE TABLE test_questions (
  test_id      BIGINT NOT NULL REFERENCES tests(id)     ON DELETE CASCADE,
  question_id  BIGINT NOT NULL REFERENCES questions(id) ON DELETE RESTRICT,
  position     INT NOT NULL DEFAULT 0,
  PRIMARY KEY (test_id, question_id),
  CONSTRAINT uq_test_question_order UNIQUE (test_id, position) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX idx_test_questions_question ON test_questions (question_id);
