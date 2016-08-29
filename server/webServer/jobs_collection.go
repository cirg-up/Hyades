package main

import (
	"database/sql"
	"log"

	"github.com/HeinOldewage/Hyades"

	_ "github.com/mattn/go-sqlite3"
)

type JobMap struct {
	dbFile string
}

func NewJobMap(dbFile string) *JobMap {
	return &JobMap{dbFile}
}

func (jm *JobMap) NewJob(user *Hyades.Person) *Hyades.Job {
	return &Hyades.Job{OwnerID: user.Id}
}

func (jm *JobMap) GetJob(id string) (job *Hyades.Job, err error) {
	conn, err := sql.Open("sqlite3", "file:"+jm.dbFile+"?_loc=auto")
	if err != nil {
		return nil, err
	}
	res, err := conn.Query("Select * from JOBS where ID = ?", id)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	job = new(Hyades.Job)
	if res.Next() {
		res.Scan(&job.Id, &job.OwnerID, &job.Name, &job.JobFolder, &job.Env, &job.ReturnEnv)
	}

	log.Println("JobID", job.Id)

	partres, err := conn.Query("Select Id,DispatchTime,FinishTime,TotalTimeDispatched,Done,Dispatched,BeingHandled,FailCount,Error,Status,Command from JOBPARTS where OwnerID = ?", job.Id)
	if err != nil {
		log.Println(err)
	}
	for partres.Next() {
		var part *Hyades.Work = Hyades.NewWork(job)

		err := partres.Scan(&part.PartID, &part.DispatchTime, &part.FinishTime, &part.TotalTimeDispatched, &part.Done, &part.Dispatched, &part.BeingHandled, &part.FailCount, &part.Error, &part.Status, &part.Command)
		if err != nil {
			log.Println("partres.Scan", err)
		}

		log.Println("PartId", part.PartID)

		paramres, err := conn.Query("Select Parameters from Parameters where JOBPARTSID = ?", part.PartID)
		for paramres.Next() {
			var param string
			err := paramres.Scan(&param)
			if err != nil {
				log.Println("paramres.Scan", err)
			}
			part.Parameters = append(part.Parameters, param)
		}
	}

	if partres.Err() != nil {
		log.Println(partres.Err())
	}

	log.Println("Job has", len(job.Parts), " parts")

	return job, err
}

func (jm *JobMap) GetAll(user *Hyades.Person) (jobs []*Hyades.Job, err error) {
	conn, err := sql.Open("sqlite3", "file:"+jm.dbFile+"?_loc=auto")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	res, err := conn.Query("Select * from JOBS where OwnerID = ?", user.Id)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	for res.Next() {
		job := &Hyades.Job{}
		err := res.Scan(&job.Id, &job.OwnerID, &job.Name, &job.JobFolder, &job.Env, &job.ReturnEnv)
		if err != nil {
			log.Println(err)
		}
		partres, err := conn.Query("Select Id,DispatchTime,FinishTime,TotalTimeDispatched,Done,Dispatched,BeingHandled,FailCount,Error,Status,Command from JOBPARTS where OwnerID = ?", job.Id)
		if err != nil {
			log.Println(err)
		}
		for partres.Next() {
			var part Hyades.Work

			err := partres.Scan(&part.PartID, &part.DispatchTime, &part.FinishTime, &part.TotalTimeDispatched, &part.Done, &part.Dispatched, &part.BeingHandled, &part.FailCount, &part.Error, &part.Status, &part.Command)
			if err != nil {
				log.Println("partres.Scan", err)
			}

			paramres, err := conn.Query("Select Parameters from Parameters where JOBPARTSID = ?", part.PartID)
			for paramres.Next() {
				var param string
				err := paramres.Scan(&param)
				if err != nil {
					log.Println("paramres.Scan", err)
				}
				part.Parameters = append(part.Parameters, param)
			}

		}

		if partres.Err() != nil {
			log.Println(partres.Err())
		}

		jobs = append(jobs, job)
	}

	return jobs, res.Err()
}

func (jm *JobMap) AddJob(job *Hyades.Job) error {
	conn, err := sql.Open("sqlite3", "file:"+jm.dbFile+"?_loc=auto")
	if err != nil {
		return err
	}
	trans, err := conn.Begin()
	if err != nil {
		return err
	}
	defer trans.Rollback()

	res, err := trans.Exec("Insert into JOBS (OwnerID,Name,JobFolder,Env,ReturnEnv) values (  ? , ? , ? , ? , ? );", &job.OwnerID, &job.Name, &job.JobFolder, &job.Env, &job.ReturnEnv)
	if err != nil {
		return err
	}

	id, err := res.LastInsertId()
	if err == nil {
		job.Id = int(id)
		log.Println("Got id back")
	} else {
		log.Println("Cannot get job ID", err)
	}

	for _, part := range job.Parts {
		//JOBPARTS (Id,OwnerID,DispatchTime,FinishTime,TotalTimeDispatched,CompletedBy,CurrentClient,Done,Dispatched,BeingHandled,FailCount,Error,Status,Command)
		res, err := trans.Exec("Insert into JOBPARTS (OwnerID,DispatchTime,FinishTime,TotalTimeDispatched,CompletedBy,CurrentClient,Done,Dispatched,BeingHandled,FailCount,Error,Status,Command)"+
			" values (  ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ? , ?);", id, part.DispatchTime, part.FinishTime, part.TotalTimeDispatched, 0, 0, part.Done, part.Dispatched, part.BeingHandled, part.FailCount, part.Error, part.Status, part.Command)
		if err != nil {
			return err
		}

		partid, err := res.LastInsertId()
		if err == nil {
			part.PartID = int32(partid)
		}

		for _, param := range part.Parameters {
			_, err := trans.Exec("Insert into Parameters (JOBPARTSID,Parameters) values (  ? , ?  );", part.PartID, param)
			if err != nil {
				return err
			}
		}

	}

	trans.Commit()
	return nil
}
