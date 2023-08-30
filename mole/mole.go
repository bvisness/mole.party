package mole

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const BASE_URL = "https://mole.party/"
const WS_BASE_URL = "wss://mole.party/"

// const BASE_URL = "http://localhost:8080/"
// const WS_BASE_URL = "ws://localhost:8080/"

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Conn struct {
	ID        string
	Send      chan string
	ExpiresAt time.Time
}

var conns sync.Map

func trashConn(id string) {
	iconn, ok := conns.LoadAndDelete(id)
	if !ok {
		return
	}
	conn := iconn.(Conn)
	close(conn.Send)

	log.Print("Trashed connection ", id)
}

//go:embed templates
var templateFS embed.FS
var templates = map[string]*template.Template{}
var templateFuncs = template.FuncMap{
	"url": func(s string) template.URL {
		u, err := url.JoinPath(BASE_URL, s)
		if err != nil {
			log.Printf("ERROR: failed to make URL: %v", err)
			return ""
		}
		return template.URL(u)
	},
	"wsurl": func(s string) template.URL {
		u, err := url.JoinPath(WS_BASE_URL, s)
		if err != nil {
			log.Printf("ERROR: failed to make URL: %v", err)
			return ""
		}
		return template.URL(u)
	},
}

//go:embed static
var staticFS embed.FS
var staticServer http.Handler

func init() {
	addTemplate := func(name string) {
		t := template.New(name)
		t.Funcs(templateFuncs)
		t, err := t.ParseFS(templateFS, fmt.Sprintf("templates/%s", name), "templates/base.html")
		if err != nil {
			panic(err)
		}
		templates[name] = t
	}

	addTemplate("index.html")
	addTemplate("send.html")

	staticServer = http.FileServer(http.FS(staticFS))
}

func RunApp() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !(r.Method == http.MethodGet && r.URL.Path == "/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		conn := Conn{
			ID:        uuid.New().String(),
			Send:      make(chan string),
			ExpiresAt: time.Now().Add(time.Minute * 10),
		}
		conns.Store(conn.ID, conn)

		err := templates["index.html"].Execute(w, map[string]any{
			"ID": conn.ID,
		})
		if err != nil {
			log.Printf("ERROR: couldn't render index: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	})
	http.HandleFunc("/listen", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ERROR: couldn't upgrade websocket: %v", err)
			return
		}

		type listenMsg struct {
			ID string `json:"id"`
		}
		var msg listenMsg
		if err := ws.ReadJSON(&msg); err != nil {
			log.Printf("ERROR: couldn't read listen message from client: %v", err)
			return
		}

		var conn Conn
		if iconn, ok := conns.Load(msg.ID); ok {
			conn = iconn.(Conn)
		} else {
			ws.Close()
			return
		}

		log.Print("Started connection ", conn.ID)

		url := <-conn.Send
		if url == "" {
			ws.Close()
			return
		}

		ws.WriteJSON(map[string]any{
			"url": url,
		})

		log.Print("Sent url to client for connection ", conn.ID)

		ws.Close()
		trashConn(conn.ID)
	})
	http.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			err := templates["send.html"].Execute(w, nil)
			if err != nil {
				log.Printf("ERROR: couldn't render send page: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		case http.MethodPost:
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("ERROR: couldn't read send body: %v", err)
				return
			}

			type Body struct {
				ID  string `json:"id"`
				Url string `json:"url"`
			}
			var body Body
			if err := json.Unmarshal(bodyBytes, &body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			iconn, ok := conns.Load(body.ID)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			conn := iconn.(Conn)

			if time.Now().After(conn.ExpiresAt) {
				w.WriteHeader(http.StatusGone)
				return
			}

			conn.Send <- body.Url
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
	})
	http.Handle("/static/", staticServer)

	go func() {
		t := time.NewTicker(time.Minute)
		for range t.C {
			var idsToDelete []string
			conns.Range(func(key, value any) bool {
				conn := value.(Conn)
				if time.Now().After(conn.ExpiresAt.Add(time.Minute)) {
					idsToDelete = append(idsToDelete, conn.ID)
				}
				return true
			})
			for _, id := range idsToDelete {
				log.Print("Connection is expired: ", id)
				trashConn(id)
			}
		}
	}()

	log.Print("Listening on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
