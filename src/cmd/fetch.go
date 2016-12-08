package main

import (
	"flag"
	"fmt"
	"github.com/IMQS/log"
	"jira"
	"os"
	"time"
	"timedb"
	"tmetric"
)

type Fetcher interface {
	Name() string
	LoadConfig() error
	Fetch(db *timedb.TimeDB, start, end time.Time) error
	FetchRaw(start, end time.Time) ([]byte, error)
}

func init() {
}

func main() {
	// Fetch this much history, every time we fetch
	// We fetch from midnight of the current day (current time zone), to X time before midnight tonight.
	historyDays := flag.Int("days", 0, "Number of days of history to fetch")
	doJIRA := flag.Bool("jira", true, "Enable fetching JIRA tickets")
	doTMetric := flag.Bool("tmetric", true, "Enable fetching TMetric values")
	flag.Parse()

	if *historyDays <= 0 {
		fmt.Printf("days is less than 1. Not doing anything\n")
		os.Exit(0)
	}

	logger := log.New("scraper.log")
	//logger := log.New(log.Stdout)

	db := &timedb.TimeDB{}
	db.Log = logger
	if err := db.LoadConfig(); err != nil {
		fmt.Printf("%v", err)
		os.Exit(1)
	}
	if err := db.Connect(); err != nil {
		fmt.Printf("Error connecting to time db: %v", err)
		os.Exit(1)
	}

	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)

	start_date := midnight.Add(time.Duration(-*historyDays*24) * time.Hour)
	end_date := midnight
	//past = time.Date(2016, time.December, 6, 0, 0, 0, 0, time.UTC)
	//now = time.Date(2016, time.December, 7, 0, 0, 0, 0, time.UTC)

	fetchers := []Fetcher{}
	if *doJIRA {
		fmt.Printf("JIRA enabled\n")
		fetchers = append(fetchers, &jira.Fetcher{})
	}
	if *doTMetric {
		fmt.Printf("TMetric enabled\n")
		fetchers = append(fetchers, &tmetric.Fetcher{})
	}

	// We don't want to continue through errors, because if JIRA fetches fail, then
	// tmetric will end up creating a whole bunch of anonymous tasks.
	var err error
	for _, f := range fetchers {
		if err = f.LoadConfig(); err != nil {
			logger.Errorf("Error loading config for %v:\n%v\n", f.Name(), err)
			break
		}
		if err = f.Fetch(db, start_date, end_date); err != nil {
			logger.Errorf("Error fetching from %v:\n%v\n", f.Name(), err)
			break
		}
	}

	if err == nil {
		logger.Infof("Finished successfully\n")
	} else {
		logger.Infof("Finished with errors\n")
		os.Exit(1)
	}
}
