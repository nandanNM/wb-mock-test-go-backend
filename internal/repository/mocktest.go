package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pointsPerCorrect is the points awarded per correct answer. Central knob.
const pointsPerCorrect = 10

// Mock-test errors.
var (
	ErrAlreadySubmitted = errors.New("attempt already submitted")
	ErrTestUnavailable  = errors.New("test is not available")
	ErrNoQuestions      = errors.New("test has no questions")
	ErrInvalidAnswer    = errors.New("answer references an option not in the question")
)

// QuizOption is a single-language option shown to the test-taker (no is_correct).
type QuizOption struct {
	ID       int64  `json:"id"`
	Position int16  `json:"position"`
	Body     string `json:"body"`
}

// QuizQuestion is a single-language question for taking a test (no answer key).
type QuizQuestion struct {
	ID       int64        `json:"id"`
	Position int          `json:"position"`
	Prompt   string       `json:"prompt"`
	Options  []QuizOption `json:"options"`
}

// SubmitAnswer is a caller-supplied answer. SelectedOptionID nil = skipped.
type SubmitAnswer struct {
	QuestionID       int64  `json:"question_id"`
	SelectedOptionID *int64 `json:"selected_option_id"`
}

// AnswerKeyItem is one graded question in the result.
type AnswerKeyItem struct {
	QuestionID       int64  `json:"question_id"`
	SelectedOptionID *int64 `json:"selected_option_id,omitempty"`
	CorrectOptionID  *int64 `json:"correct_option_id,omitempty"`
	IsCorrect        bool   `json:"is_correct"`
}

// AttemptResult is the graded outcome of an attempt.
type AttemptResult struct {
	AttemptID       int64           `json:"attempt_id"`
	TestID          int64           `json:"test_id"`
	Score           int             `json:"score"`
	TotalQuestions  int             `json:"total_questions"`
	Correct         int             `json:"correct"`
	Incorrect       int             `json:"incorrect"`
	Skipped         int             `json:"skipped"`
	Accuracy        float64         `json:"accuracy"`
	PointsEarned    int             `json:"points_earned"`
	DurationSeconds *int            `json:"duration_seconds,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	CurrentStreak   int             `json:"current_streak"`
	LongestStreak   int             `json:"longest_streak"`
	AnswerKey       []AnswerKeyItem `json:"answer_key,omitempty"`
}

// MockTestRepository implements the take-test flow: start, fetch questions,
// grade+submit (transactional, updates streak/points), and read results.
type MockTestRepository struct{ pool *pgxpool.Pool }

func NewMockTestRepository(pool *pgxpool.Pool) *MockTestRepository {
	return &MockTestRepository{pool}
}

// StartAttempt validates the test is published and non-empty, then creates an
// in-progress attempt, returning its id, start time and question count.
func (r *MockTestRepository) StartAttempt(ctx context.Context, userID string, testID int64, lang string) (int64, time.Time, int, error) {
	var published bool
	err := r.pool.QueryRow(ctx, `SELECT is_published FROM tests WHERE id=$1`, testID).Scan(&published)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, 0, ErrNotFound
	}
	if err != nil {
		return 0, time.Time{}, 0, err
	}
	if !published {
		return 0, time.Time{}, 0, ErrTestUnavailable
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM test_questions WHERE test_id=$1`, testID).Scan(&total); err != nil {
		return 0, time.Time{}, 0, err
	}
	if total == 0 {
		return 0, time.Time{}, 0, ErrNoQuestions
	}

	var id int64
	var startedAt time.Time
	err = r.pool.QueryRow(ctx,
		`INSERT INTO test_attempts (user_id, test_id, language_code, total_questions)
		 VALUES ($1, $2, $3, $4) RETURNING id, started_at`,
		userID, testID, lang, total).Scan(&id, &startedAt)
	return id, startedAt, total, err
}

// QuestionsForTest returns the test's questions (in the given language, ordered)
// without any answer-key information.
func (r *MockTestRepository) QuestionsForTest(ctx context.Context, testID int64, lang string) ([]QuizQuestion, error) {
	qRows, err := r.pool.Query(ctx, `
		SELECT q.id, tq.position,
		       CASE WHEN $2 = 'bn' THEN q.prompt_bn ELSE q.prompt_en END
		FROM test_questions tq JOIN questions q ON q.id = tq.question_id
		WHERE tq.test_id = $1 ORDER BY tq.position`, testID, lang)
	if err != nil {
		return nil, err
	}
	defer qRows.Close()

	questions := make([]QuizQuestion, 0)
	index := map[int64]int{} // question id -> slice index
	for qRows.Next() {
		var q QuizQuestion
		if err := qRows.Scan(&q.ID, &q.Position, &q.Prompt); err != nil {
			return nil, err
		}
		q.Options = []QuizOption{}
		index[q.ID] = len(questions)
		questions = append(questions, q)
	}
	if err := qRows.Err(); err != nil {
		return nil, err
	}
	if len(questions) == 0 {
		return questions, nil
	}

	oRows, err := r.pool.Query(ctx, `
		SELECT qo.question_id, qo.id, qo.position,
		       CASE WHEN $2 = 'bn' THEN qo.body_bn ELSE qo.body_en END
		FROM question_options qo
		WHERE qo.question_id IN (SELECT question_id FROM test_questions WHERE test_id = $1)
		ORDER BY qo.question_id, qo.position`, testID, lang)
	if err != nil {
		return nil, err
	}
	defer oRows.Close()
	for oRows.Next() {
		var qid int64
		var o QuizOption
		if err := oRows.Scan(&qid, &o.ID, &o.Position, &o.Body); err != nil {
			return nil, err
		}
		if i, ok := index[qid]; ok {
			questions[i].Options = append(questions[i].Options, o)
		}
	}
	return questions, oRows.Err()
}

// SubmitAttempt grades the submitted answers, persists them, finalizes the
// attempt, and updates the user's streak + points — all in one transaction.
func (r *MockTestRepository) SubmitAttempt(ctx context.Context, userID string, attemptID int64, answers []SubmitAnswer, durationSeconds *int) (AttemptResult, error) {
	var res AttemptResult
	res.AttemptID = attemptID

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		// Claim the attempt (owner, not yet completed).
		var testID int64
		var completedAt *time.Time
		err := tx.QueryRow(ctx,
			`SELECT test_id, completed_at FROM test_attempts WHERE id=$1 AND user_id=$2 FOR UPDATE`,
			attemptID, userID).Scan(&testID, &completedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if completedAt != nil {
			return ErrAlreadySubmitted
		}
		res.TestID = testID

		// Load grading data: correct option + valid options per question.
		correct := map[int64]int64{}        // question -> correct option
		valid := map[int64]map[int64]bool{} // question -> set of option ids
		rows, err := tx.Query(ctx, `
			SELECT tq.question_id, qo.id, qo.is_correct
			FROM test_questions tq JOIN question_options qo ON qo.question_id = tq.question_id
			WHERE tq.test_id = $1`, testID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var qid, oid int64
			var isCorrect bool
			if err := rows.Scan(&qid, &oid, &isCorrect); err != nil {
				rows.Close()
				return err
			}
			if valid[qid] == nil {
				valid[qid] = map[int64]bool{}
			}
			valid[qid][oid] = true
			if isCorrect {
				correct[qid] = oid
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		total := len(valid)

		// Index submitted answers; validate they belong to the test/question.
		submitted := map[int64]*int64{}
		for _, a := range answers {
			if valid[a.QuestionID] == nil {
				continue // not a question in this test — ignore
			}
			if a.SelectedOptionID != nil && !valid[a.QuestionID][*a.SelectedOptionID] {
				return ErrInvalidAnswer
			}
			submitted[a.QuestionID] = a.SelectedOptionID
		}

		// Grade every question in the test and persist a row per question.
		score, answered := 0, 0
		res.AnswerKey = make([]AnswerKeyItem, 0, total)
		for qid := range valid {
			sel := submitted[qid]
			isCorrect := sel != nil && correct[qid] != 0 && *sel == correct[qid]
			if sel != nil {
				answered++
			}
			if isCorrect {
				score++
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO attempt_answers (attempt_id, question_id, selected_option_id, is_correct)
				 VALUES ($1,$2,$3,$4) ON CONFLICT (attempt_id, question_id) DO NOTHING`,
				attemptID, qid, sel, isCorrect); err != nil {
				return err
			}
			co := correct[qid]
			var coPtr *int64
			if co != 0 {
				coPtr = &co
			}
			res.AnswerKey = append(res.AnswerKey, AnswerKeyItem{
				QuestionID: qid, SelectedOptionID: sel, CorrectOptionID: coPtr, IsCorrect: isCorrect,
			})
		}
		points := score * pointsPerCorrect

		// Finalize the attempt (atomic guard against double-submit).
		tag, err := tx.Exec(ctx, `
			UPDATE test_attempts
			SET score=$2, points_earned=$3, duration_seconds=$4, completed_at=now()
			WHERE id=$1 AND completed_at IS NULL`,
			attemptID, score, points, durationSeconds)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrAlreadySubmitted
		}

		// Update streak + total points. Streak: +1 if last active yesterday,
		// unchanged if already active today, else reset to 1.
		err = tx.QueryRow(ctx, `
			UPDATE users u
			SET current_streak = ns.cs,
			    longest_streak = GREATEST(u.longest_streak, ns.cs),
			    last_active_on = CURRENT_DATE,
			    total_points = u.total_points + $2
			FROM (
			    SELECT CASE
			        WHEN last_active_on = CURRENT_DATE THEN current_streak
			        WHEN last_active_on = CURRENT_DATE - 1 THEN current_streak + 1
			        ELSE 1 END AS cs
			    FROM users WHERE id = $1
			) ns
			WHERE u.id = $1
			RETURNING u.current_streak, u.longest_streak`,
			userID, points).Scan(&res.CurrentStreak, &res.LongestStreak)
		if err != nil {
			return err
		}

		res.Score = score
		res.TotalQuestions = total
		res.Correct = score
		res.Incorrect = answered - score
		res.Skipped = total - answered
		res.PointsEarned = points
		res.DurationSeconds = durationSeconds
		if total > 0 {
			res.Accuracy = float64(int((float64(score)/float64(total))*10000+0.5)) / 100
		}
		now := time.Now()
		res.CompletedAt = &now
		return nil
	})
	if err != nil {
		return AttemptResult{}, err
	}
	return res, nil
}

// GetResult returns a completed attempt with its full answer key, for the owner.
func (r *MockTestRepository) GetResult(ctx context.Context, userID string, attemptID int64) (AttemptResult, error) {
	var (
		res         AttemptResult
		accuracy    float64
		completedAt *time.Time
		duration    *int
		score       int
		total       int
	)
	err := r.pool.QueryRow(ctx, `
		SELECT test_id, score, total_questions, accuracy::float8, points_earned, duration_seconds, completed_at
		FROM test_attempts WHERE id=$1 AND user_id=$2`, attemptID, userID).
		Scan(&res.TestID, &score, &total, &accuracy, &res.PointsEarned, &duration, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptResult{}, ErrNotFound
	}
	if err != nil {
		return AttemptResult{}, err
	}
	res.AttemptID = attemptID
	res.Score, res.Correct, res.TotalQuestions = score, score, total
	res.Accuracy, res.DurationSeconds, res.CompletedAt = accuracy, duration, completedAt

	// Answer key with correct option per question.
	rows, err := r.pool.Query(ctx, `
		SELECT aa.question_id, aa.selected_option_id, aa.is_correct,
		       (SELECT id FROM question_options WHERE question_id = aa.question_id AND is_correct) AS correct_option
		FROM attempt_answers aa WHERE aa.attempt_id = $1 ORDER BY aa.question_id`, attemptID)
	if err != nil {
		return AttemptResult{}, err
	}
	defer rows.Close()
	answered := 0
	res.AnswerKey = make([]AnswerKeyItem, 0)
	for rows.Next() {
		var it AnswerKeyItem
		if err := rows.Scan(&it.QuestionID, &it.SelectedOptionID, &it.IsCorrect, &it.CorrectOptionID); err != nil {
			return AttemptResult{}, err
		}
		if it.SelectedOptionID != nil {
			answered++
		}
		res.AnswerKey = append(res.AnswerKey, it)
	}
	res.Incorrect = answered - score
	res.Skipped = total - answered
	return res, rows.Err()
}

// UserStats is the user's aggregate analytics.
type UserStats struct {
	TotalPoints       int64      `json:"total_points"`
	CurrentStreak     int        `json:"current_streak"`
	LongestStreak     int        `json:"longest_streak"`
	LastActiveOn      *time.Time `json:"last_active_on,omitempty"`
	TotalAttempts     int        `json:"total_attempts"`
	CompletedAttempts int        `json:"completed_attempts"`
	AvgAccuracy       float64    `json:"avg_accuracy"`
	TotalCorrect      int64      `json:"total_correct"`
}

// Stats returns the user's gamification + performance analytics.
func (r *MockTestRepository) Stats(ctx context.Context, userID string) (UserStats, error) {
	var s UserStats
	err := r.pool.QueryRow(ctx, `
		SELECT total_points, current_streak, longest_streak, last_active_on
		FROM users WHERE id=$1`, userID).
		Scan(&s.TotalPoints, &s.CurrentStreak, &s.LongestStreak, &s.LastActiveOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserStats{}, ErrNotFound
	}
	if err != nil {
		return UserStats{}, err
	}
	err = r.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE completed_at IS NOT NULL),
		       COALESCE(avg(accuracy) FILTER (WHERE completed_at IS NOT NULL), 0)::float8,
		       COALESCE(sum(score) FILTER (WHERE completed_at IS NOT NULL), 0)
		FROM test_attempts WHERE user_id=$1`, userID).
		Scan(&s.TotalAttempts, &s.CompletedAttempts, &s.AvgAccuracy, &s.TotalCorrect)
	return s, err
}
