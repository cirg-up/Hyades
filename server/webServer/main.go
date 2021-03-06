package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/HeinOldewage/Hyades"
	//"github.com/gorilla/context"

	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/sessions"

	"github.com/boltdb/bolt"
	"github.com/yosssi/boltstore/reaper"
	"github.com/yosssi/boltstore/store"

	"github.com/HeinOldewage/Hyades/server/databaseDefinition"
)

type ConfigFile struct {
	DataPath      *string
	ServerAddress *string
	DB            *string
}

var configFilePath *string = flag.String("config", "config.json", "If the config file is specified it overrides command line paramters and defaults")

var configuration ConfigFile = ConfigFile{
	DataPath:      flag.String("dataFolder", "userData", "The folder that the distribution server saves the data"),
	ServerAddress: flag.String("address", ":8088", "The folder that the distribution server saves the data"),
	DB:            flag.String("DBserver", "127.0.0.1:8085", "Sqlite db file"),
}

func main() {
	fmt.Println("This is the web server")
	flag.Parse()

	if *configFilePath != "" {
		fmt.Println("Loading config file", *configFilePath)
		file, err := os.Open(*configFilePath)
		if err != nil {
			log.Println(err)
			return
		}

		decoder := json.NewDecoder(file)
		err = decoder.Decode(&configuration)
		if err != nil {
			log.Println(err)
			return
		}
	}

	log.Println("config", *configuration.DataPath, *configuration.DB, *configuration.ServerAddress)

	log.Println("Starting web server on ", *configuration.ServerAddress)
	submitServer := NewSubmitServer(*configuration.ServerAddress, *configuration.DB)
	submitServer.Listen()
}

const usersFileName string = "users.gob"

type SubmitServer struct {
	Address     string
	JobServer   interface{}
	Cookiestore *sessions.CookieStore

	jobs   *JobMap
	users  *UserMap
	boltDB *bolt.DB
}

func NewSubmitServer(Address string, server string) *SubmitServer {

	//Delete all previous Jobs, After Users are saved/loaded from file only delete if that fails

	userMap := NewUserMap("users.db")

	defer log.Println("NewSubmitServer Done")

	// Open a Bolt database.
	db, err := bolt.Open("./sessions.db", 0666, nil)
	if err != nil {
		panic(err)
	}
	jm, err := NewJobMap(server)
	if err != nil {
		panic(err)
	}
	return &SubmitServer{Address,
		nil,
		sessions.NewCookieStore([]byte("ForTheUnity")),
		jm,
		userMap,
		db,
	}

}

func (ss *SubmitServer) Listen() {

	http.HandleFunc("/submit", ss.securePage(ss.submitJob))
	http.HandleFunc("/stop", ss.securePage(ss.stopJob))
	http.HandleFunc("/start", ss.securePage(ss.startJob))
	http.HandleFunc("/Jobs", ss.securePage(ss.listJobs))
	http.HandleFunc("/JobStatus", ss.securePage(ss.jobStatus))
	http.HandleFunc("/DeleteJob", ss.securePage(ss.deleteJob))

	http.HandleFunc("/GetJobResult", ss.securePage(ss.getJobResult))
	http.HandleFunc("/CreateJob", ss.securePage(ss.createJob))
	http.HandleFunc("/Help", ss.securePage(ss.help))
	http.HandleFunc("/About", ss.securePage(ss.about))
	http.HandleFunc("/Admin", ss.securePage(ss.admin))
	http.HandleFunc("/Logout", ss.securePage(ss.logoutUser))

	http.HandleFunc("/Observe/Get/", ss.securePage(ss.observe))

	http.HandleFunc("/Observe/New/", ss.securePage(ss.addObserver))

	http.HandleFunc("/TryLogin", ss.loginUser)
	http.HandleFunc("/TryRegister", ss.newUser)

	http.Handle("/", http.StripPrefix("/", http.FileServer(http.Dir("resources/files"))))

	log.Println("Starting SubmitServer")

	defer reaper.Quit(reaper.Run(ss.boltDB, reaper.Options{}))

	err := http.ListenAndServe(ss.Address, nil)
	if err != nil {
		panic(err)
	}
}

func (ss *SubmitServer) submitJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	Env, Envfh, _ := req.FormFile("Env")
	descr, _, _ := req.FormFile("workDescr")
	if !(Env == nil || Envfh == nil) && descr != nil {

		descrReader := bufio.NewReader(descr)

		job := &Hyades.Job{OwnerID: user.Id}

		decodeError := json.NewDecoder(descrReader).Decode(job)
		log.Println("Creating job for user with id", user.Id, " And name", user.Username)
		job.OwnerID = user.Id
		job.JobFolder = user.Username

		if decodeError != nil {
			http.Error(w, decodeError.Error(), http.StatusBadRequest)
			return
		}

		//Save envBytes to file
		folder := filepath.Join(*configuration.DataPath, "EnvFiles", user.Username)
		os.MkdirAll(folder, os.ModePerm|os.ModeDir)
		filename := filepath.Join(folder, job.Name+fmt.Sprint(time.Now().Unix())+"env.zip")

		file, err := os.Create(filename)
		if err != nil {
			log.Println("(ss *SubmitServer) submitJob, Create", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer file.Close()
		_, err = io.Copy(file, Env)
		if err != nil {
			log.Println(" (ss *SubmitServer) submitJob", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job.Env = filename

		log.Println("About to call ss.Jobs().AddJob(job)")
		dbJob := &databaseDefinition.Job{}
		databaseDefinition.LoadInto(dbJob, job)
		dbParts := make([]*databaseDefinition.Work, len(job.Parts))
		for k := 0; k < len(dbParts); k++ {
			dbParts[k] = &databaseDefinition.Work{}
			databaseDefinition.LoadInto(dbParts[k], job.Parts[k])
			dbParts[k].PartOfID = dbJob.GetId()
		}
		//dbJob.NumParts = int64(len(dbParts))
		err = ss.Jobs().AddJob(dbJob, dbParts)
		if err != nil {
			log.Println("ss.Jobs().AddJob(job):", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("Job created", job.Id)
	} else {

		log.Println("File not correctly uploaded")
	}
}

func GetSubject(val reflect.Value, path []string) (interface{}, error) {
	if val.Type().Kind() == reflect.Ptr {
		val = reflect.Indirect(val)
	}

	parts := strings.Split(path[0], "*")
	element := parts[0]
	var query []string
	if len(parts) > 1 {
		query = parts[1:]
	}
	Fieldval := val.FieldByName(element)
	if Fieldval.IsValid() {
		val = Fieldval
	} else {
		//Maybe it is not a field
		MethVal := val.MethodByName(element)
		if !MethVal.IsValid() {
			for k := 0; k < val.Type().NumField(); k++ {
				log.Println(val.Type().Field(k).Name)
			}
			for k := 0; k < val.Type().NumMethod(); k++ {
				log.Println(val.Type().Method(k).Name)
			}
			return nil, errors.New(fmt.Sprint("Could not find a field or method with the name ", element, " on ", val.Type()))
		} else {
			val = MethVal
		}
		if val.Type().NumOut() == 0 {
			return nil, errors.New(fmt.Sprint("Function of type", val.Type(), " does not return a value"))
		}
		if val.Type().NumIn() == 0 {
			val = val.Call([]reflect.Value{})[0]
		}
	}

	if len(query) != 0 {
		switch val.Type().Kind() {
		case reflect.Slice, reflect.Array:
			{
				index, err := strconv.Atoi(query[0])
				if err != nil {
					return nil, err
				}
				val = val.Index(index)
			}
		case reflect.Map:
			{
				var key reflect.Value
				switch val.Type().Key().Kind() {
				case reflect.Int:
					{
						i, err := strconv.Atoi(query[0])
						if err != nil {
							return nil, err
						}
						key = reflect.ValueOf(i)
					}
				case reflect.String:
					{
						key = reflect.ValueOf(query[0])
					}
				}
				val = val.MapIndex(key)
				if !val.IsValid() {
					return nil, errors.New(fmt.Sprint("Map does not contain ", key.Interface()))
				}
			}
		}

	}

	if len(path) > 1 {
		return GetSubject(val, path[1:])
	} else {
		if val.CanInterface() {
			res := val.Interface()
			return res, nil
		} else {
			return nil, errors.New(fmt.Sprint("Cannot access ", val.Type().Name()))
		}
	}
}

func (ss *SubmitServer) addObserver(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	path := strings.Split(req.URL.Path, "/")
	path = path[3:]
	log.Println(path)

	val, err := GetSubject(reflect.ValueOf(ss), path)
	if err != nil {
		json.NewEncoder(w).Encode(err.Error())
		return
	}
	subject, ok := val.(Hyades.Observable)
	if !ok {
		fmt.Fprintf(w, "Object (%T) not observable", val)
		return
	}
	observer := subject.AddObserver()
	json.NewEncoder(w).Encode(observer.Id)

}

func (ss *SubmitServer) observe(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		log.Println(req.Form)
		http.Error(w, "Observer id not provided", http.StatusNotFound)
		return
	}
	obID, err := strconv.Atoi(id[0])
	if err != nil {
		log.Println(id)
		http.Error(w, "Observer id malformed"+id[0], http.StatusNotFound)
		return

	}

	path := strings.Split(req.URL.Path, "/")
	path = path[3:]
	log.Println(path)

	val, err := GetSubject(reflect.ValueOf(ss), path)
	if err != nil {
		json.NewEncoder(w).Encode(err.Error())
		return
	}
	subject, ok := val.(Hyades.Observable)
	if !ok {
		fmt.Fprintln(w, "Object not observable")
		return
	}

	changes, ok := subject.GetChanges(uint32(obID))
	if !ok {
		http.Error(w, "Observer does not exist", http.StatusNotFound)
		return

	}
	json.NewEncoder(w).Encode(changes)

}

func (ss SubmitServer) Jobs() *JobMap {
	return ss.jobs
}

func (ss *SubmitServer) stopJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		log.Println(req.Form)
		http.Error(w, "Job id not provided", http.StatusNotFound)
		return
	}
	log.Println("Stopping", id[0])

	//TODO :: Notify
	/*	if job, ok := ss.jobs.GetJob(id[0]); ok {

			//ss.JobServer.StopJob(job)
		} else {
			log.Println("Failed to find job", id[0], "in map")

	}*/

}

func (ss *SubmitServer) startJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		log.Println(req.Form)
		http.Error(w, "Job id not provided", http.StatusNotFound)
		return
	}
	log.Println("Starting", id[0])

	//TODO :: Notify
	/*
		if job, ok := ss.jobs.GetJob(id[0]); ok {

			//ss.JobServer.StartJob(job)
		} else {
			log.Println("Failed to find job", id[0], "in map")

		}*/
}

func (ss *SubmitServer) createJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	var fm template.FuncMap = make(template.FuncMap)

	fm["currentTab"] = func() string {
		return "createJob"
	}
	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("resources/templates/frame.html", "resources/templates/createJobs/header.html",
		"resources/templates/nav.html", "resources/templates/createJobs/body.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pageData := map[string]interface{}{"NavData": user, "HeaderData": nil, "BodyData": nil}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		log.Println(err)
	}
}

func (ss *SubmitServer) listJobs(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	var fm template.FuncMap = make(template.FuncMap)

	jobs, err := ss.jobs.GetAllWithoutWork(user)

	fm["IDToString"] = func(id int64) string {
		return fmt.Sprint(id)
	}

	fm["CountDone"] = func(id int64) string {

		/*job, err := ss.jobs.GetJob(id)
		if err != nil {
			log.Println("listJobs_CountDone", err, id)
			return ""
		}*/
		for j := range jobs {
			if jobs[j].Id == id {
				return fmt.Sprint(jobs[j].NumParts)
			}

		}
		return fmt.Sprint(0) //NumPartsDone(job)
	}
	fm["totalWork"] = func(id int64) string {

		/*job, err := ss.jobs.GetJob(id)
		if err != nil {
			log.Println("listJobs_totalWork", err, id)
			return ""
		}*/
		for j := range jobs {
			if jobs[j].Id == id {
				return fmt.Sprint(jobs[j].NumPartsDone)
			}

		}
		return fmt.Sprint(0) //len(job.Parts)
	}

	fm["currentTab"] = func() string {
		return "listJobs"
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("resources/templates/frame.html", "resources/templates/listJobs/body.html",
		"resources/templates/listJobs/listJob.html", "resources/templates/listJobs/header.html", "resources/templates/nav.html")
	if err != nil {
		log.Println("Template parse error:", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err != nil {
		log.Println("ss.jobs.GetAll(user)", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pageData := map[string]interface{}{"NavData": user, "HeaderData": nil, "BodyData": jobs}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		log.Println("jobsTemplate.Execute", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) jobStatus(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		log.Println(req.Form)
		http.Error(w, "Job id not provided", http.StatusNotFound)
		return
	}

	var fm template.FuncMap = make(template.FuncMap)
	fm["IDToString"] = func(id int64) string {

		return fmt.Sprint(id)
	}

	fm["CountDone"] = func(id int64) string {

		job, err := ss.jobs.GetJob(id)
		if err != nil {
			log.Println("listJobs_CountDone", err, id)
			return ""
		}
		return fmt.Sprint(NumPartsDone(job))
	}
	fm["currentTab"] = func() string {
		return ""
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("resources/templates/frame.html", "resources/templates/jobStatus/header.html",
		"resources/templates/jobStatus/body.html", "resources/templates/jobStatus/statusWork.html", "resources/templates/nav.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jid, _ := strconv.ParseInt(id[0], 10, 64)
	job, err := ss.jobs.GetJob(jid)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pageData := map[string]interface{}{"NavData": user, "HeaderData": job, "BodyData": job}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) deleteJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	log.Println("deleteJob")
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		http.Error(w, "Job id not provided", http.StatusNotFound)
		return
	}
	jid, _ := strconv.ParseInt(id[0], 10, 64)
	job, err := ss.jobs.GetJob(jid)
	if err != nil {
		log.Println("id[0]", id[0])
		log.Println("getJobResult - ss.jobs.GetJob", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = ss.Jobs().Delete(job)
	if err != nil {
		log.Println("id[0]", id[0], "job.Id", job.Id)
		log.Println("getJobResult - ss.Jobs().Delete", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) getJobResult(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		http.Error(w, "Job id not provided", http.StatusNotFound)
		return
	}
	jid, _ := strconv.ParseInt(id[0], 10, 64)
	job, err := ss.jobs.GetJob(jid)
	if err != nil {
		log.Println("id[0]", id[0])
		log.Println("getJobResult - ss.jobs.GetJob", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Println("job.JobFolder", job.JobFolder)
	log.Println("user.Username", user.Username)

	TempJobFolder := filepath.Join(*configuration.DataPath, job.JobFolder, job.Name+fmt.Sprint(job.Id))

	zipedfilePath := filepath.Join(*configuration.DataPath, job.JobFolder, "Job"+job.Name+fmt.Sprint(job.Id)+".zip")
	log.Println("Creating zip at", zipedfilePath)
	zipedFile, err := os.OpenFile(zipedfilePath, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		log.Println("Error creating file", zipedfilePath, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stat, err := zipedFile.Stat()
	if err == nil && stat.Size() == 0 {
		log.Println("About to compress", TempJobFolder)
		Hyades.ZipCompressFolderWriter(TempJobFolder, zipedFile)
		log.Println("Zipped file")
	}

	http.ServeContent(w, req, "Job"+job.Name+".zip", time.Now(), zipedFile)

}

func (ss *SubmitServer) help(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	var fm template.FuncMap = make(template.FuncMap)

	fm["currentTab"] = func() string {
		return "help"
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("resources/templates/frame.html", "resources/templates/help/body.html",
		"resources/templates/help/header.html", "resources/templates/nav.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pageData := map[string]interface{}{"NavData": user, "HeaderData": nil, "BodyData": ss.jobs}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) about(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	var fm template.FuncMap = make(template.FuncMap)
	fm["currentTab"] = func() string {
		return "about"
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("resources/templates/frame.html", "resources/templates/about/body.html",
		"resources/templates/about/header.html", "resources/templates/nav.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pageData := map[string]interface{}{"NavData": user, "HeaderData": nil, "BodyData": nil}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) admin(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	if !user.Admin {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	var fm template.FuncMap = make(template.FuncMap)
	fm["currentTab"] = func() string {
		return "admin"
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("resources/templates/frame.html", "resources/templates/admin/body.html",
		"resources/templates/admin/header.html", "resources/templates/nav.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pageData := map[string]interface{}{"NavData": user, "HeaderData": nil, "BodyData": ss.jobs, "Users": ss.users}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) logoutUser(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {

	session, err := ss.Cookiestore.Get(req, "Session")
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)

		return
	}

	session.Values["sessID"] = ""
	session.Save(req, w)

	javascriptredirect(w, "/")
}

func (ss *SubmitServer) newUser(w http.ResponseWriter, req *http.Request) {
	session, err := ss.Cookiestore.Get(req, "Session")

	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	req.ParseForm()
	if len(req.PostForm["Name"]) == 0 || len(req.PostForm["Password"]) == 0 {
		log.Println("Name or password missing:", req.PostForm)
		http.Error(w, "Name or password missing", http.StatusUnauthorized)
		return
	}

	_, ok := ss.users.addUser(req.PostForm["Name"][0], req.PostForm["Password"][0])

	if ok {
		log.Println("New user added")

		sessID := strconv.FormatInt(time.Now().Unix(), 10)
		session.Values["sessID"] = sessID

		log.Println("!!New session!!")
	} else {
		log.Println("!!Username already in use!!")
		http.Error(w, "Username already in use", http.StatusUnauthorized)
	}
	session.Save(req, w)
}

func (ss *SubmitServer) loginUser(w http.ResponseWriter, req *http.Request) {

	str, err := store.New(ss.boltDB, store.Config{}, []byte("secret-key"))
	if err != nil {

		log.Println(err)
		http.Error(w, "Internal error", http.StatusInternalServerError)

		return
	}
	session, err := str.New(req, "session")

	if err != nil {
		log.Println("str.New(req, Session)", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	req.ParseForm()
	if len(req.PostForm["Name"]) == 0 || len(req.PostForm["Password"]) == 0 {
		log.Println("Name or password missing:", req.PostForm)
		http.Error(w, "Name or password missing", http.StatusUnauthorized)
		return
	}

	u, ok := ss.users.findUser(req.PostForm["Name"][0], req.PostForm["Password"][0])

	if ok {
		session.Values["sessID"] = u.Username
		// Create a session.

		log.Println("!!New session!!", u.Username)
	} else {
		log.Println("!!Invalid username/password on login!!")
		http.Error(w, "Not a valid username or password", http.StatusUnauthorized)
	}
	if err := session.Save(req, w); err != nil {
		log.Println("Saving session failed")
	}
}

func (ss *SubmitServer) securePage(toRun func(runuser *Hyades.Person, w http.ResponseWriter, req *http.Request)) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		str, err := store.New(ss.boltDB, store.Config{}, []byte("secret-key"))
		if err != nil {

			log.Println("securePage store.New", req.URL.Path, "err:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		session, err := str.Get(req, "session")
		if err != nil {

			log.Println("securePage", req.URL.Path, "err:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if SessIDut, ok := session.Values["sessID"]; ok {

			var SessID string
			switch t := SessIDut.(type) {
			case string:
				SessID = t //SessIDut.(string)
			default:
				http.Error(w, "SessID invalid type", http.StatusInternalServerError)
			}

			//runuser, ok := ss.sessionUserMap[SessID]
			runuser := ss.users.getUser(SessID)
			if runuser != nil {
				toRun(runuser, w, req)
			} else {
				javascriptredirect(w, "/?to="+req.URL.RequestURI())
				//http.Error(w, "/", http.StatusTemporaryRedirect )

			}

		} else {
			log.Println("Can't get session ID")
			javascriptredirect(w, "/?to="+req.URL.RequestURI())
			//http.Error(w, "/", http.StatusTemporaryRedirect )
		}
		session.Save(req, w)
	}

}

func javascriptredirect(w io.Writer, path string) {
	writer := bufio.NewWriter(w)
	writer.WriteString("<!DOCTYPE html><html><script type=\"text/javascript\" >")
	writer.WriteString("location.assign(\"" + path + "\") </script></html>\n")
	err := writer.Flush()
	if err != nil {
		log.Println("javascriptredirect", err)
	}
}

func NumPartsDone(j *Hyades.Job) int {
	count := 0
	for w := range j.Parts {
		if j.Parts[w].Done {
			count++
		}
	}
	return count
}
