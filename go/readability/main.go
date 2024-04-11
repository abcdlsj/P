package main

import (
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis"
	readability "github.com/go-shiori/go-readability"
	"github.com/gorilla/mux"
)

type article struct {
	URL     string
	Title   string
	Content string
	ErrMsg  string
}

var (
	//go:embed *.html
	tmplFiles embed.FS

	//go:embed style.css
	cssFile embed.FS

	funcMap = template.FuncMap{
		"safeHTML": func(content string) template.HTML {
			return template.HTML(content)
		},
	}

	tmpl = template.Must(template.New("article.html").Funcs(funcMap).ParseFS(tmplFiles, "article.html", "index.html"))

	REDIS_URL = os.Getenv("REDIS_URL")

	redisclient *redis.Client
)

func init() {
	opt, _ := redis.ParseURL(REDIS_URL)
	redisclient = redis.NewClient(opt)

	if err := redisclient.Ping().Err(); err != nil {
		log.Fatalf("Failed to connect to redis, URL: %s, error: %s", REDIS_URL, err.Error())
	}
}

func main() {
	r := mux.NewRouter()
	r.SkipClean(true)

	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.FS(cssFile))))

	r.HandleFunc("/", indexHandler)
	r.PathPrefix("/read/").HandlerFunc(readHandler)
	r.PathPrefix("/read").Methods("POST").HandlerFunc(readRedirectHandler)

	log.Fatal(http.ListenAndServe(port(), r))
}

func port() string {
	if port := os.Getenv("PORT"); port != "" {
		log.Println("Listening on address, http://localhost:" + port)
		return ":" + port
	}

	log.Println("Listening on address, http://localhost:8080")
	return ":8080"
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	last10arts, err := getLastNArticles(10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	err = tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
		"Recents": last10arts,
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func readRedirectHandler(w http.ResponseWriter, r *http.Request) {
	uri := r.FormValue("url")
	http.Redirect(w, r, "/read/"+escape(uri), http.StatusSeeOther)
}

func escape(s string) string {
	replacer := strings.NewReplacer(
		"/", "%2F",
	)

	return replacer.Replace(s)
}

func unescape(s string) string {
	replacer := strings.NewReplacer(
		"%2F", "/",
	)

	return replacer.Replace(s)
}

func readHandler(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.EscapedPath()[len("/read/"):]

	if uri == "" {
		http.NotFound(w, r)
		return
	}

	uri = unescape(uri)

	render(w, readabyFormURL(uri))
}

func readabyFormURL(uri string) article {
	if art, err := getArticleFromCache(uri); err != nil || art != nil {
		return *art
	}

	fromdata, err := readability.FromURL(uri, 30*time.Second)
	if err != nil {
		return article{URL: uri, ErrMsg: err.Error()}
	}

	art := article{URL: uri, Title: fromdata.Title, Content: fromdata.Content}

	defer setArticleToCache(uri, art)
	return art
}

func render(w http.ResponseWriter, data article) {
	err := tmpl.ExecuteTemplate(w, "article.html", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func setArticleToCache(key string, art article) error {
	data, err := json.Marshal(art)
	if err != nil {
		return err
	}

	defer lpushToRedis(key)

	return redisclient.Set(key, data, 0).Err()
}

func getArticleFromCache(key string) (*article, error) {
	var data []byte

	if err := redisclient.Get(key).Scan(&data); err != nil {
		if err == redis.Nil {
			return nil, nil
		}

		return &article{URL: key, ErrMsg: err.Error()}, errors.New("failed to get article from cache")
	}

	var art article
	if err := json.Unmarshal(data, &art); err != nil {
		return &article{URL: key, ErrMsg: err.Error()}, errors.New("failed to unmarshal article from json")
	}

	log.Printf("get article from cache: %s", key)
	defer incrViewCount(key)

	return &art, nil
}

func incrViewCount(key string) error {
	return redisclient.ZIncrBy("readability-viewcount", 1, key).Err()
}

func lpushToRedis(key string) error {
	return redisclient.LPush("readability-timequeue", key).Err()
}

func getLastNArticles(n int) ([]string, error) {
	records := make([]string, 0, n)

	if err := redisclient.LRange("readability-timequeue", 0, int64(n)).ScanSlice(&records); err != nil {
		log.Printf("failed to get last %d articles from redis: %s", n, err.Error())
		return nil, err
	}

	return records, nil
}
