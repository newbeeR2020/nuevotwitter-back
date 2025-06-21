package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	firebase "firebase.google.com/go/v4"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
)

var db *sql.DB
var fbApp *firebase.App

// ---------- DB 初期化 ----------
func initDB() {
	godotenv.Load()
	dsn := os.Getenv("DATABASE_URL")
	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
}

// ---------- Firebase 初期化 ----------
func initFirebase() {
	var err error
	cfg := &firebase.Config{
		ProjectID: "uttc-hackathon-9202d",
	}
	fbApp, err = firebase.NewApp(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
}

// ---------- 認証ミドルウェア ----------
func auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") {
			http.Error(w, "unauth", http.StatusUnauthorized)
			return
		}
		idToken := hdr[7:] // “Bearer …”
		client, err := fbApp.Auth(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		token, err := client.VerifyIDToken(r.Context(), idToken)
		if err != nil {
			http.Error(w, "unauth", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), "uid", token.UID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---------- ハンドラ ----------

// c. POST /api/tweets
type TweetIn struct {
	Text           string `json:"text"`
	ConversationID string `json:"conversationId,omitempty"`
	ReplyToID      string `json:"replyToId,omitempty"`
	QuotedID       string `json:"quotedId,omitempty"`
}

func createTweet(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value("uid").(string)
	var in TweetIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	_, err := db.Exec(`
		INSERT INTO tweets
		  (id,text,author_id,conversation_id,reply_to_id,quoted_id)
		VALUES (UUID(),?,?,?,?,?)`,
		in.Text, uid,
		ifEmpty(in.ConversationID), in.ReplyToID, in.QuotedID)
	if err != nil {
		log.Println("insert tweet:", err)
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// d. GET /api/tweets
func listTweets(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT id,text,author_id,like_count,created_at
		FROM tweets ORDER BY created_at DESC LIMIT 20`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type Resp struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		AuthorID  string `json:"authorId"`
		LikeCount uint   `json:"likeCount"`
		CreatedAt string `json:"createdAt"`
	}
	var out []Resp
	for rows.Next() {
		var r Resp
		rows.Scan(&r.ID, &r.Text, &r.AuthorID, &r.LikeCount, &r.CreatedAt)
		out = append(out, r)
	}
	json.NewEncoder(w).Encode(out)
}

// e. POST /api/tweets/{id}/like
func likeTweet(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value("uid").(string)
	tid := mux.Vars(r)["id"]

	tx, _ := db.Begin()
	res, err := tx.Exec(
		`INSERT IGNORE INTO likes (user_id,tweet_id) VALUES (?,?)`, uid, tid)
	if err != nil {
		tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	aff, _ := res.RowsAffected()
	if aff == 1 {
		if _, err := tx.Exec(`UPDATE tweets SET like_count = like_count+1 WHERE id=?`, tid); err != nil {
			tx.Rollback()
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
	tx.Commit()
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/tweets/{parentId}/reply
func replyTweet(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value("uid").(string)
	pid := mux.Vars(r)["parentId"]
	var in TweetIn
	_ = json.NewDecoder(r.Body).Decode(&in)

	var convid string
	if err := db.QueryRow(`SELECT conversation_id FROM tweets WHERE id=?`, pid).Scan(&convid); err != nil {
		http.Error(w, "parent tweet not found", 404)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := tx.Exec(`
		INSERT INTO tweets
		  (id,text,author_id,conversation_id,reply_to_id,quoted_id)
		VALUES (UUID(),?,?,?,?,?)`,
		in.Text, uid,
		convid, pid, in.QuotedID); err != nil {
		tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := tx.Exec(`
		UPDATE tweets SET reply_count=reply_count+1 WHERE id=?`, pid); err != nil {
		tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	tx.Commit()
	w.WriteHeader(http.StatusCreated)
}

// ---------- ヘルパ ----------
func ifEmpty(cID string) string {
	if cID == "" { // 新スレッドなら自分の UUID が後で入る想定
		row := db.QueryRow(`SELECT UUID()`)
		row.Scan(&cID)
	}
	return cID
}

// ---------- エントリポイント ----------
func main() {
	initDB()
	initFirebase()
	r := mux.NewRouter()
	r.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	}).Methods("GET")
	api := r.PathPrefix("/api").Subrouter()
	api.Use(auth)
	api.HandleFunc("/tweets", createTweet).Methods("POST")
	api.HandleFunc("/tweets", listTweets).Methods("GET")
	api.HandleFunc("/tweets/{id}/like", likeTweet).Methods("POST")
	api.HandleFunc("/tweets/{parentId}/reply", replyTweet).Methods("POST")
	cors := handlers.CORS(
		handlers.AllowedOrigins([]string{
			"http://localhost:5173",                 // Vite dev server
			"https://nuevotwitter-front.vercel.app", // 本番フロント
		}),
		handlers.AllowedMethods([]string{"GET", "POST", "OPTIONS"}),
		handlers.AllowedHeaders([]string{
			"Content-Type",
			"Authorization",
		}),
		handlers.AllowCredentials(), // Cookie を使うなら
	)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}
	log.Printf("listen :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, cors(r)))
}
