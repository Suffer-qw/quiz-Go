package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID       int64
	Username string
	Role     string
}

type Test struct {
	ID          int64
	Title       string
	Description string
	CreatedAt   time.Time
}

type Question struct {
	ID          int64
	TestID      int64
	Text        string
	Options     [4]string
	Correct     int
	Position    int
}

type AttemptListItem struct {
	ID          int64
	TestTitle   string
	StartedAt   time.Time
	FinishedAt  time.Time
	Total       int
	Correct     int
	Percent     int
}

type GradeResult struct {
	AttemptID int64 `json:"attemptId"`
	Total     int   `json:"total"`
	Correct   int   `json:"correct"`
	Percent   int   `json:"percent"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if _, err := d.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return nil, err
	}
	return &Store{db: d}, nil
}

func (s *Store) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS tests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS questions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			text TEXT NOT NULL,
			opt0 TEXT NOT NULL,
			opt1 TEXT NOT NULL,
			opt2 TEXT NOT NULL,
			opt3 TEXT NOT NULL,
			correct_index INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS attempts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
			started_at DATETIME NOT NULL,
			finished_at DATETIME NOT NULL,
			total_questions INTEGER NOT NULL,
			correct_answers INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_user_time ON attempts(user_id, finished_at DESC);`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Seed() error {
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users;`).Scan(&cnt); err != nil {
		return err
	}
	if cnt > 0 {
		return nil
	}
	// Default accounts:
	// admin / admin123
	// user  / user123
	if _, err := s.CreateUser("admin", "admin123", "admin"); err != nil {
		return err
	}
	if _, err := s.CreateUser("user", "user123", "user"); err != nil {
		return err
	}

	// Seed one sample test with different question count.
	testID, err := s.createTest("Пример теста: Go базовый", "Небольшой тест на 3 вопроса.")
	if err != nil {
		return err
	}
	qs := []struct {
		Text    string
		Opts    [4]string
		Correct int
	}{
		{
			Text: "Какой пакет используется для HTTP сервера в стандартной библиотеке?",
			Opts: [4]string{"net/http", "http", "golang/http", "std/http"},
			Correct: 0,
		},
		{
			Text: "Как объявить переменную в Go?",
			Opts: [4]string{"let x = 1", "var x int", "int x = 1", "x := 1; var"},
			Correct: 1,
		},
		{
			Text: "Какой тип у rune?",
			Opts: [4]string{"uint8", "int32", "string", "byte"},
			Correct: 1,
		},
	}
	for i, q := range qs {
		if _, err := s.db.Exec(
			`INSERT INTO questions(test_id, position, text, opt0, opt1, opt2, opt3, correct_index)
			 VALUES(?,?,?,?,?,?,?,?);`,
			testID, i+1, q.Text, q.Opts[0], q.Opts[1], q.Opts[2], q.Opts[3], q.Correct,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateUser(username, password, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("empty username")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO users(username, password_hash, role, created_at) VALUES(?,?,?,?);`,
		username, string(hash), role, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, Role: role}, nil
}

func (s *Store) Authenticate(username, password string) (*User, error) {
	var u User
	var hash string
	err := s.db.QueryRow(
		`SELECT id, username, password_hash, role FROM users WHERE username = ?;`,
		strings.TrimSpace(username),
	).Scan(&u.ID, &u.Username, &hash, &u.Role)
	if err != nil {
		return nil, errors.New("bad credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, errors.New("bad credentials")
	}
	return &u, nil
}

func (s *Store) CreateSession(w http.ResponseWriter, userID int64, maxAge time.Duration) error {
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	expires := now.Add(maxAge)
	if _, err := s.db.Exec(
		`INSERT INTO sessions(token, user_id, expires_at, created_at) VALUES(?,?,?,?);`,
		token, userID, expires, now,
	); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
	return nil
}

func (s *Store) DeleteSession(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie("session")
	if err == nil && c.Value != "" {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token = ?;`, c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
	return nil
}

func (s *Store) GetUserByRequest(r *http.Request) (*User, error) {
	c, err := r.Cookie("session")
	if err != nil || c.Value == "" {
		return nil, err
	}
	var u User
	var expires time.Time
	err = s.db.QueryRow(
		`SELECT u.id, u.username, u.role, sess.expires_at
		 FROM sessions sess
		 JOIN users u ON u.id = sess.user_id
		 WHERE sess.token = ?;`, c.Value,
	).Scan(&u.ID, &u.Username, &u.Role, &expires)
	if err != nil {
		return nil, err
	}
	if time.Now().UTC().After(expires) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token = ?;`, c.Value)
		return nil, errors.New("expired")
	}
	return &u, nil
}

func (s *Store) ListTests() ([]Test, error) {
	rows, err := s.db.Query(`SELECT id, title, description, created_at FROM tests ORDER BY created_at DESC, id DESC;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Test
	for rows.Next() {
		var t Test
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) GetTestWithQuestions(testID int64) (*Test, []Question, error) {
	var t Test
	if err := s.db.QueryRow(`SELECT id, title, description, created_at FROM tests WHERE id = ?;`, testID).
		Scan(&t.ID, &t.Title, &t.Description, &t.CreatedAt); err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Query(
		`SELECT id, test_id, position, text, opt0, opt1, opt2, opt3, correct_index
		 FROM questions WHERE test_id = ? ORDER BY position ASC;`, testID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var qs []Question
	for rows.Next() {
		var q Question
		if err := rows.Scan(&q.ID, &q.TestID, &q.Position, &q.Text, &q.Options[0], &q.Options[1], &q.Options[2], &q.Options[3], &q.Correct); err != nil {
			return nil, nil, err
		}
		qs = append(qs, q)
	}
	if len(qs) == 0 {
		return nil, nil, errors.New("no questions")
	}
	return &t, qs, nil
}

func (s *Store) GradeAndStoreAttempt(userID, testID int64, answers map[string]int) (*GradeResult, error) {
	t, qs, err := s.GetTestWithQuestions(testID)
	_ = t
	if err != nil {
		return nil, err
	}
	total := len(qs)
	correct := 0
	for _, q := range qs {
		key := strconv.FormatInt(q.ID, 10)
		if v, ok := answers[key]; ok && v == q.Correct {
			correct++
		}
	}
	percent := 0
	if total > 0 {
		percent = int(float64(correct) / float64(total) * 100.0)
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO attempts(user_id, test_id, started_at, finished_at, total_questions, correct_answers)
		 VALUES(?,?,?,?,?,?);`,
		userID, testID, now, now, total, correct,
	)
	if err != nil {
		return nil, err
	}
	attemptID, _ := res.LastInsertId()

	return &GradeResult{
		AttemptID: attemptID,
		Total:     total,
		Correct:   correct,
		Percent:   percent,
	}, nil
}

func (s *Store) ListRecentAttempts(userID int64, limit int) ([]AttemptListItem, error) {
	rows, err := s.db.Query(
		`SELECT a.id, t.title, a.started_at, a.finished_at, a.total_questions, a.correct_answers
		 FROM attempts a
		 JOIN tests t ON t.id = a.test_id
		 WHERE a.user_id = ?
		 ORDER BY a.finished_at DESC
		 LIMIT ?;`, userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptListItem
	for rows.Next() {
		var it AttemptListItem
		if err := rows.Scan(&it.ID, &it.TestTitle, &it.StartedAt, &it.FinishedAt, &it.Total, &it.Correct); err != nil {
			return nil, err
		}
		it.Percent = 0
		if it.Total > 0 {
			it.Percent = int(float64(it.Correct) / float64(it.Total) * 100.0)
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *Store) CreateTestFromAdmin(title, description string, questions []AdminQuestion) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	res, err := tx.Exec(`INSERT INTO tests(title, description, created_at) VALUES(?,?,?);`, title, description, now)
	if err != nil {
		return 0, err
	}
	testID, _ := res.LastInsertId()

	for i, q := range questions {
		_, err := tx.Exec(
			`INSERT INTO questions(test_id, position, text, opt0, opt1, opt2, opt3, correct_index)
			 VALUES(?,?,?,?,?,?,?,?);`,
			testID, i+1, strings.TrimSpace(q.Text),
			q.Options[0], q.Options[1], q.Options[2], q.Options[3],
			q.CorrectIndex,
		)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return testID, nil
}

func (s *Store) createTest(title, description string) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO tests(title, description, created_at) VALUES(?,?,?);`, title, description, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

