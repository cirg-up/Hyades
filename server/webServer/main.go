package main

import (

	"github.com/HeinOldewage/Hyades"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	//"github.com/gorilla/context"

	"github.com/gorilla/sessions"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	fmt.Println("This is the web server")
	/*
	http.Handle("/", http.FileServer(http.Dir("./resources")))
	err := http.ListenAndServe(":80", nil)
	if err != nil {
		panic(err)
	}*/
	submitServer := NewSubmitServer(":80", nil)
	submitServer.Listen()
}


const dataPath string = "userData"
const usersFileName string = "users.gob"

type SubmitServer struct {
	Address        string
	JobServer      interface{}
	Cookiestore    *sessions.CookieStore
	sessionUserMap map[string]*Hyades.Person

	jobs      *JobMap
	users     *UserMap
	observers *Hyades.ObserverList
}

func NewSubmitServer(Address string, js interface{}) *SubmitServer {

	//Delete all previous Jobs, After Users are saved/loaded from file only delete if that fails
	userMap, err := NewUserMap(usersFileName)
	if err != nil {
		log.Println(err)
		log.Println("Creating Blank Submit Server")
		os.RemoveAll(dataPath)
	} else {
		log.Println("Loading Submit Server")
	}

	defer log.Println("NewSubmitServer Done")
	return &SubmitServer{Address,
		js,
		sessions.NewCookieStore([]byte("ForTheUnity")),
		make(map[string]*Hyades.Person),
		NewJobMap(),
		userMap,
		Hyades.NewObserverList(),
	}

}

func (ss *SubmitServer) Listen() {
	/*http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println(r.URL.Path)
		fmt.Fprintf(w, "Hello, %q", html.EscapeString(r.URL.Path))
	})*/

	http.HandleFunc("/submit", ss.securePage(ss.submitJob))
	http.HandleFunc("/stop", ss.securePage(ss.stopJob))
	http.HandleFunc("/start", ss.securePage(ss.startJob))
	http.HandleFunc("/Jobs", ss.securePage(ss.listJobs))
	http.HandleFunc("/JobStatus", ss.securePage(ss.jobStatus))
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

	http.Handle("/", http.StripPrefix("/", http.FileServer(http.Dir("web/files"))))

	log.Println("Starting SubmitServer")
	err := http.ListenAndServe(ss.Address, nil)
	if err != nil {
		panic(err)
	}
}

func (ss *SubmitServer) submitJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	Env, Envfh, _ := req.FormFile("Env")
	windows, _, _ := req.FormFile("windowsCMD")
	linux, _, _ := req.FormFile("linuxCMD")
	descr, _, _ := req.FormFile("workDescr")
	if !(Env == nil || Envfh == nil) {
		envBytes, _ := ioutil.ReadAll(Env)
		windowsReader := bufio.NewReader(windows)
		linuxReader := bufio.NewReader(linux)
		descrReader := bufio.NewReader(descr)
		job := ss.jobs.NewJob(user)
		job.Env = envBytes

		var k int
		for k = 1; ; k++ {
			wcmd, werr := windowsReader.ReadString('\n')
			lcmd, lerr := linuxReader.ReadString('\n')
			if (werr == nil) && (lerr == nil) {
				id := fmt.Sprint(k)
				if descr != nil {
					id, _ = descrReader.ReadString('\n')
					id = strings.Trim(id, "\n\r")

				}
				wcmd = strings.Trim(wcmd, "\n\r")
				lcmd = strings.Trim(lcmd, "\n\r")
				work := Hyades.NewWork(job, id, wcmd, lcmd, req.FormValue("ReturnEnv") == "true")

				log.Println("Adding work", wcmd, lcmd, req.FormValue("ReturnEnv"))
				job.AddWork(work)

			} else {
				break
			}

		}
		ss.JobServer.StartJob(job)
		ss.jobs.AddJob(job)
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
		fmt.Fprintf(w, "Object (%T) not observable",val)
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

	if job, ok := ss.jobs.GetJob(id[0]); ok {
		ss.JobServer.StopJob(job)
	} else {
		log.Println("Failed to find job", id[0], "in map")

	}

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

	if job, ok := ss.jobs.GetJob(id[0]); ok {
		ss.JobServer.StartJob(job)
	} else {
		log.Println("Failed to find job", id[0], "in map")

	}
}

func (ss *SubmitServer) createJob(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	var fm template.FuncMap = make(template.FuncMap)

	fm["currentTab"] = func() string {
		return "createJob"
	}
	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("web/templates/frame.html", "web/templates/createJobs/header.html",
		"web/templates/nav.html", "web/templates/createJobs/body.html")
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
	fm["CountDone"] = func(id string) string {
		job, _ := ss.jobs.GetJob(id)
		return fmt.Sprint(atomic.LoadInt32(&job.NumPartsDone))
	}
	fm["totalWork"] = func(id string) string {
		job, _ := ss.jobs.GetJob(id)
		return fmt.Sprint(len(job.Parts))
	}

	fm["currentTab"] = func() string {
		return "listJobs"
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("web/templates/frame.html", "web/templates/listJobs/body.html",
		"web/templates/listJobs/listJob.html", "web/templates/listJobs/header.html", "web/templates/nav.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pageData := map[string]interface{}{"NavData": user, "HeaderData": nil, "BodyData": ss.jobs.GetAll()}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (ss *SubmitServer) jobStatus(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	id, ok := req.Form["id"]
	if !ok {
		http.Error(w, "Job id not provided", http.StatusNotFound)
		return
	}
	log.Println(req.Form)
	var fm template.FuncMap = make(template.FuncMap)
	fm["CountDone"] = func(id string) string {
		job, _ := ss.jobs.GetJob(id)
		return fmt.Sprint(atomic.LoadInt32(&job.NumPartsDone))
	}
	fm["currentTab"] = func() string {
		return ""
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("web/templates/frame.html", "web/templates/jobStatus/header.html",
		"web/templates/jobStatus/body.html", "web/templates/jobStatus/statusWork.html", "web/templates/nav.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	job, _ := ss.jobs.GetJob(id[0])
	pageData := map[string]interface{}{"NavData": user, "HeaderData": job, "BodyData": job}
	err = jobsTemplate.Execute(w, pageData)

	if err != nil {
		log.Println(err)
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

	job, _ := ss.jobs.GetJob(id[0])

	TempJobFolder := filepath.Join("userData", job.Owner.JobFolder, job.JobID)
	retEnv := Hyades.ZipCompressFolder(TempJobFolder)
	log.Println(TempJobFolder, "getJobResult bytes:", len(retEnv))
	http.ServeContent(w, req, "Job"+job.JobID+".zip", time.Now(), bytes.NewReader(retEnv))

}

func (ss *SubmitServer) help(user *Hyades.Person, w http.ResponseWriter, req *http.Request) {
	var fm template.FuncMap = make(template.FuncMap)

	fm["currentTab"] = func() string {
		return "help"
	}

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("web/templates/frame.html", "web/templates/help/body.html",
		"web/templates/help/header.html", "web/templates/nav.html")
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

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("web/templates/frame.html", "web/templates/about/body.html",
		"web/templates/about/header.html", "web/templates/nav.html")
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

	jobsTemplate, err := template.New("frame.html").Funcs(fm).ParseFiles("web/templates/frame.html", "web/templates/admin/body.html",
		"web/templates/admin/header.html", "web/templates/nav.html")
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

	u, ok := ss.users.addUser(req.PostForm["Name"][0], req.PostForm["Password"][0])

	if ok {
		log.Println("New user added")
		err := ss.users.Save(usersFileName)
		if err != nil {
			log.Println("Could not save user map")
			log.Println(err)
		} else {
			log.Println("Successfully saved user map")
		}

		sessID := strconv.FormatInt(time.Now().Unix(), 10)
		session.Values["sessID"] = sessID

		ss.sessionUserMap[sessID] = u

		log.Println("!!New session!!")
	} else {
		log.Println("!!Username already in use!!")
		http.Error(w, "Username already in use", http.StatusUnauthorized)
	}
	session.Save(req, w)
}

func (ss *SubmitServer) loginUser(w http.ResponseWriter, req *http.Request) {
	session, err := ss.Cookiestore.New(req, "Session")

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

	u, ok := ss.users.findUser(req.PostForm["Name"][0], req.PostForm["Password"][0])

	if ok {

		sessID := strconv.FormatInt(time.Now().Unix(), 10)
		session.Values["sessID"] = sessID

		ss.sessionUserMap[sessID] = u

		log.Println("!!New session!!")
	} else {
		log.Println("!!Invalid username/password on login!!")
		http.Error(w, "Not a valid username or password", http.StatusUnauthorized)
	}
	session.Save(req, w)
}

func (ss *SubmitServer) securePage(toRun func(runuser *Hyades.Person, w http.ResponseWriter, req *http.Request)) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		session, err := ss.Cookiestore.Get(req, "Session")
		if err != nil {
			log.Println("securePage", req.URL.Path, "err:", err)
		}
		if SessIDut, ok := session.Values["sessID"]; ok {

			var SessID string
			switch t := SessIDut.(type) {
			case string:
				SessID = t //SessIDut.(string)
			default:
				http.Error(w, "SessID invalid type", http.StatusInternalServerError)
			}

			runuser, ok := ss.sessionUserMap[SessID]
			if ok {
				toRun(runuser, w, req)
			} else {
				javascriptredirect(w, "/?to="+req.URL.RequestURI())
				//http.Error(w, "/", http.StatusTemporaryRedirect )

			}

		} else {
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
