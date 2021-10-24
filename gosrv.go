package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	_ "github.com/lib/pq"

	"github.com/gomodule/redigo/redis"

	"github.com/gorilla/mux"
	"net/http"
)

type JsonResponse struct {
	Id   string `json:"id"`
	Info string `json:"info"`
	Data string `json:"telemetry"`
	Flag string `json:"is_online"`
}

type config struct {
	Ids ids `toml:"ids"`
}

type ids struct {
	File string `toml:"file"`
	Ttl  int64  `toml:"ttl"`
}

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "postgres"
	dbname   = "id"
)

// global variables
var ttl int64 = 30
var last int64 = time.Now().Unix()
var idsm = make(map[string]bool)
var infm = make(map[string]string)
var telm = make(map[string]string)
var isom = make(map[string]time.Time)
var msgt = make(chan string)
var msgi = make(chan string)
var msgu = make(chan string)

func main() {

	log.SetLevel(log.InfoLevel)
	go Timer()

	if 2 > len(os.Args) {
		fmt.Println("usage:")
		fmt.Println("gosrv conf.toml")
		os.Exit(2)
	}
	var conf config
	if _, err := toml.DecodeFile(os.Args[1], &conf); err != nil {
		fmt.Println(err)
		return
	}

	idFile := conf.Ids.File
	ttl = conf.Ids.Ttl
	log.Info("get devices id from: ", idFile, " timeout to update status: ", ttl)

	// read devices id, fill up maps
	f, err := os.OpenFile(idFile, os.O_RDONLY, os.ModePerm)
	if err != nil {
		log.Fatalf("open file error: %v", err)
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		id := sc.Text()
		idsm[id] = false
		isom[id] = time.Now()
		log.Info(id)
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("scan file error: %v", err)
		return
	}

	// keydb connection
	cl, err := redis.Dial("tcp", "localhost:6379")
	if err != nil {
		log.Fatal(err)
	}
	defer cl.Close()
	log.Info("Connected to noSQL DB!")

	// http server
	router := mux.NewRouter()

	// SQL conncetion
	psqlconn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbname)
	db, err := sql.Open("postgres", psqlconn)
	CheckError(err)
	defer db.Close()

	err = db.Ping()
	CheckError(err)
	log.Info("Connected to SQL DB!")

	// (re)create registered ID's table and fill it by ID's from config file
	_, e0 := db.Query("DROP TABLE devices;")
	CheckError(e0)

	_, e1 := db.Query("CREATE TABLE devices (id BIGSERIAL PRIMARY KEY, is_online BOOL, info JSONB);")
	CheckError(e1)

	for key, _ := range idsm {
		// init device table records
		_, e := db.Exec(`INSERT INTO "devices"("id", "is_online","info") VALUES ($1, $2, '{}')`, key, false)
		CheckError(e)

		infm[key] = "{}"
		telm[key] = "{}"
		// init http server endpoints
		dev := "/device/"
		epg := dev + key
		epi := dev + key + "/info"
		ept := dev + key + "/telemetry"
		router.HandleFunc(epg, Get).Methods("GET")
		router.HandleFunc(epi, PostInfo).Methods("POST")
		router.HandleFunc(ept, PostTelemetry).Methods("POST")
	}

	go http.ListenAndServe(":8080", router)
	log.Info("Ready http server!")
	Update()

	for {
		select {
		case m := <-msgi:
			log.Info("info ", m) // update SQL and noSQL info
			s := strings.Split(m, "/")

			// validate json to aviod crash
			var x struct{}
			if err := json.Unmarshal([]byte(s[1]), &x); err != nil {
				log.Error(err, s[1])
			} else {
				infm[s[0]] = s[1]
				q := "UPDATE devices SET info = " + `'` + s[1] + `' ` + " WHERE id = " + s[0]
				log.Info(q)
				_, e := db.Query(q)
				CheckError(e)

				b := s[1] + "," + telm[s[0]] + `,"is_online":"` + strconv.FormatBool(idsm[s[0]]) + `"`
				log.Info(b)
				_, ec := cl.Do("SET", s[0], b)
				if ec != nil {
					log.Error(err)
				}
			}
		case m := <-msgt:
			log.Info("telemetry ", m) // update noSQL telemetry
			s := strings.Split(m, "/")
			telm[s[0]] = s[1]

			b := infm[s[0]] + "," + s[1] + `,"is_online":"` + strconv.FormatBool(idsm[s[0]]) + `"`
			log.Info(b)
			_, ec := cl.Do("SET", s[0], b)
			if ec != nil {
				log.Error(err)
			}
		case m := <-msgu:
			log.Info("update ", m) // update status noSQL and SQL

			q := "UPDATE devices SET is_online = " + `'` + strconv.FormatBool(idsm[m]) + `' ` + " WHERE id = " + m
			log.Info(q)
			_, e := db.Query(q)
			CheckError(e)

			b := infm[m] + "," + telm[m] + `,"is_online":"` + strconv.FormatBool(idsm[m]) + `"`
			log.Info(b)
			_, ec := cl.Do("SET", m, b)
			if ec != nil {
				log.Error(err)
			}
		default:
		}
	}
}

func CheckError(err error) {
	if err != nil {
		panic(err)
	}
}

func Get(w http.ResponseWriter, r *http.Request) {
	Update()
	s := strings.Split(r.URL.Path, "/")

	// read maps by id: info, telemetry, status
	if idsm[s[2]] != true {
		var response = JsonResponse{Id: s[2], Info: infm[s[2]], Data: telm[s[2]], Flag: strconv.FormatBool(idsm[s[2]])}
		json.NewEncoder(w).Encode(response)
	} else {
		var response = JsonResponse{Id: s[2], Info: infm[s[2]], Data: telm[s[2]], Flag: strconv.FormatBool(idsm[s[2]])}
		json.NewEncoder(w).Encode(response)
	}
}

func PostInfo(w http.ResponseWriter, r *http.Request) {
	Update()
	s := strings.Split(r.URL.Path, "/")
	body, _ := ioutil.ReadAll(r.Body)
	go func() { msgi <- s[2] + "/" + string(body) }()
	json.NewEncoder(w).Encode("{}")
}

func PostTelemetry(w http.ResponseWriter, r *http.Request) {
	Update()
	s := strings.Split(r.URL.Path, "/")
	body, _ := ioutil.ReadAll(r.Body)
	go func() { msgt <- s[2] + "/" + string(body) }()

	idsm[s[2]] = true
	isom[s[2]] = time.Now()
	json.NewEncoder(w).Encode("{}")
}

func Update() {
	for key, element := range idsm {
		if element != false {
			if time.Now().Unix()-isom[key].Unix() > ttl {
				idsm[key] = false
				telm[key] = "{}"
				log.Info("Updated Status for ", key)
				go func() { msgu <- key }()
			}
		}
	}
}

func Timer() {
	for {
		time.Sleep(1 * time.Second)
		Update()
		log.Debug("Timer tick ", last)
		last = time.Now().Unix()
	}
}
