package tmetric

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"
	"timedb"
)

/*
The tmetric login is convoluted, so I'm just injecting manually obtained cookies for now.

URL:

https://app.tmetric.com/api/reports/detailed/csv?
accountId=REDACTED&
activeProjectsOnly=false&
budget=false&
endDate=2016-12-06T22:00:00.000Z&
groupColumnNames=project&
groupColumnNames=user&
noRounding=false&
startDate=2016-12-04T22:00:00.000Z

Headers:
                                            actually a forward slash!
                                                      |
Accept: text/html, application/xhtml+xml, image/jxr, *\*
Accept-Encoding: gzip, deflate
Accept-Language: en-US, en-ZA; q=0.7, en; q=0.3
Connection: Keep-Alive
Cookie: _ga=REDACTED; _gat=REDACTED; .AspNet.Cookies=REDACTED
Host: app.tmetric.com
Referer: https://app.tmetric.com/
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.79 Safari/537.36 Edge/14.14393

Sample TMetric output:

User,Project,Client,Task,Tags,Time
anthony.thevenin,Team Infrastructure,,Hide / Show from Legend tool broken,,6:02:00
ben,Team Infrastructure,,AlbServer must call CrudServer on the correct port,,3:21:00


*/

const APIDateFormat = "2006-01-02T15:04:05.000Z"

// Since TMetric doesn't give us the actual start/stop numbers (only durations),
// we need to fake the start/stop. So we make all tasks start at 1am.
const TaskStartHour = 1

type Config struct {
	AccountID   string
	EmailSuffix string
	Cookies     map[string]string
}

func (c *Config) LoadFile(filename string) error {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("Error loading config file %v: %v", filename, err)
	}
	if err := json.Unmarshal(bytes, c); err != nil {
		return fmt.Errorf("Error decoding config file %v: %v", filename, err)
	}
	return nil
}

type Fetcher struct {
	Config Config
}

func (f *Fetcher) Name() string {
	return "TMetric"
}

func (f *Fetcher) LoadConfig() error {
	return f.Config.LoadFile("config/tmetric.json")
}

func roundDownToDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func (f *Fetcher) Fetch(db *timedb.TimeDB, start, end time.Time) error {
	// Split into day units, because the tmetric 'report' system that we rely upon
	// summarizes times before it gives the results back to us.
	// It's probably possible to go finer grained, but to get to the next level of
	// accuracy, such as being able to tell how many times in a day somebody has to
	// switch tasks, I think we'd need to break the granularity up pretty fine,
	// and I'm afraid of burdening the tmetric API too much.

	pos1 := roundDownToDay(start)

	for pos1.Unix() < end.Unix() {
		pos2 := pos1.Add(24 * time.Hour)
		if pos2.Unix() > end.Unix() {
			pos2 = time.Unix(end.Unix(), 0)
		}
		fmt.Printf("Fetching TMetric from %v to %v\n", pos1.Format(time.RFC3339), pos2.Format(time.RFC3339))
		if err := f.fetchInternal(db, pos1, pos2); err != nil {
			fmt.Printf("TMetric errors...")
			fmt.Println(err)
			return err
		}
		pos1 = pos2
	}

	return nil
}

func stripBOM(b []byte) []byte {
	if len(b) > 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		return b[3:]
	}
	return b
}

func (f *Fetcher) fetchInternal(db *timedb.TimeDB, start, end time.Time) error {
	//raw, err := ioutil.ReadFile("test.csv")
	raw, err := f.FetchRaw(start, end)
	if err != nil {
		return err
	}
	raw = stripBOM(raw)
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return err
	}
	// User,Project,Client,Task,Tags,Time
	// 2017-11-03 : CSV now includes additional fields if ticket was created from JIRA : Issue Id, Link
	//				These may be used to tie-up better with the JIRA extract which previously used Task without Issue Id, forcing us to string match on the description
	//				Task renamed to Time Entry
	//				
	//				EXAMPLE:
	//				﻿Day,User,Project,Project Code,Client,Time Entry,Tags,Time,Issue Id,Link 
	//				2017-10-30,ben,Team Infrastructure,,,Implement theme query API,,4:01:00,TI-2362,"=HYPERLINK(""https://imqssoftware.atlassian.net/browse/TI-2362"")"
	userPos := -1
	taskPos := -1
	timePos := -1
	times := []timedb.TimeFormat1{}
	for i, rec := range records {
		if i == 0 {
			for pos, field := range rec {
				switch field {
				case "User":
					userPos = pos
				case "Time Entry":
				// case "Task":
				 	taskPos = pos
				case "Time":
					timePos = pos
				}
			}
			if userPos == -1 {
				return fmt.Errorf("Unable to find User field in CSV. First line = '%v'", strings.Join(rec, ","))
			} else if taskPos == -1 {
				return fmt.Errorf("Unable to find Task field in CSV. First line = '%v'", strings.Join(rec, ","))
			} else if timePos == -1 {
				return fmt.Errorf("Unable to find Time field in CSV. First line = '%v'", strings.Join(rec, ","))
			}
			continue
		}
		parts := strings.Split(rec[timePos], ":")
		hour, _ := strconv.Atoi(parts[0])
		min, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		duration := time.Duration(hour*3600+min*60+sec) * time.Second

		tstart := roundDownToDay(start).Add(TaskStartHour * time.Hour)
		tend := tstart.Add(duration)
		times = append(times, timedb.TimeFormat1{
			System:    timedb.SystemTypeTMetric,
			Email:     rec[userPos] + f.Config.EmailSuffix,
			TaskTitle: rec[taskPos],
			Start:     tstart,
			End:       tend,
		})
	}

	return db.InsertTimes1(times)
}

func (f *Fetcher) FetchRaw(start, end time.Time) ([]byte, error) {
	url := "https://app.tmetric.com/api/reports/detailed/csv?"
	url += fmt.Sprintf("accountId=%v&", f.Config.AccountID)
	url += "activeProjectsOnly=false&"
	url += "budget=false&"
	url += "endDate=" + end.Format(APIDateFormat) + "&"
	url += "groupColumnNames=project&"
	url += "groupColumnNames=user&"
	url += "noRounding=false&"
	url += "startDate=" + start.Format(APIDateFormat)

	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range f.Config.Cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	req.Header.Set("Accept", "text/html, application/xhtml+xml, image/jxr, */*")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Accept-Language", "en-US, en-ZA; q=0.7, en; q=0.3")
	req.Header.Set("Host", "app.tmetric.com")
	req.Header.Set("Referer", "https://app.tmetric.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/51.0.2704.79 Safari/537.36 Edge/14.14393")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP Error: %v", resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Error reading body: %v", err)
	}
    fmt.Printf("TMetric body: %s", body)
	return body, nil
}
