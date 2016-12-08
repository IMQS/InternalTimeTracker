package jira

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
	"timedb"
)

type Config struct {
	URL      string // "https://imqssoftware.atlassian.net"
	Username string
	Password string
}

func (c *Config) LoadFile(filename string) error {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, c)
}

type Fetcher struct {
	Config Config
}

func (f *Fetcher) LoadConfig() error {
	return f.Config.LoadFile("config/jira.json")
}

func (f *Fetcher) Fetch(db *timedb.TimeDB, start, end time.Time) error {
	return f.fetchIssues(db, start, end)
}

type jiraJsonAssignee struct {
	EmailAddress string
}

type jiraJsonIssueType struct {
	Name string `json:"name"`
}

type jiraJsonFields struct {
	Assignee    jiraJsonAssignee  `json:"assignee"`
	Summary     string            `json:"summary"`
	StoryPoints float64           `json:"customfield_10004"`
	IssueType   jiraJsonIssueType `json:"issuetype"`
	Created     string            `json:"created"` //  "2016-12-05T09:55:24.000+0200"
}

type jiraJsonIssue struct {
	Id     string         `json:"id"`
	Key    string         `json:"key"`
	Fields jiraJsonFields `json:"fields"`
}

type jiraJsonResponse struct {
	StartAt int64           `json:"startAt"`
	Total   int64           `json:"total"`
	Issues  []jiraJsonIssue `json:"issues"`
}

func parseTime(jiraTime string) time.Time {
	//fmt.Printf("Parsing '%v'\n", jiraTime)
	t, _ := time.Parse("2006-01-02T15:04:05.000Z0700", jiraTime)
	return t
}

func parseIssueType(jiraType string) string {
	switch jiraType {
	case "Story":
		return timedb.TicketTypeFeature
	case "Bug":
		return timedb.TicketTypeBug
	case "BAU":
		return timedb.TicketTypeBAU
	case "Test":
		return timedb.TicketTypeTest
	case "Interrupt":
		return timedb.TicketTypeInterrupt
	case "Spike":
		return timedb.TicketTypeSpike
	case "Epic":
		return timedb.TicketTypeEpic
	}
	fmt.Printf("Unrecognized issue type %v\n", jiraType)
	return timedb.TicketTypeOther
}

func (f *Fetcher) fetchIssues(db *timedb.TimeDB, start, end time.Time) error {
	// https://imqssoftware.atlassian.net/rest/api/2/search?startAt=0&jql=created>"2016-12-07"
	offset := 0
	//start := time.Date(2016, time.November, 22, 0, 0, 0, 0, time.UTC)
	for {
		body, err := f.fetchUrl(f.Config.URL + fmt.Sprintf(`/rest/api/2/search?startAt=%v&jql=created>="%v"+AND+created<="%v"`, offset, start.Format("2006-01-02"), end.Format("2006-01-02")))
		//body, err := ioutil.ReadFile("ben-issues.json")
		if err != nil {
			return err
		}
		resp := &jiraJsonResponse{}
		if err = json.Unmarshal(body, resp); err != nil {
			return err
		}
		fmt.Printf("Fetching JIRA issues %v/%v\n", offset, resp.Total)
		if len(resp.Issues) == 0 {
			break
		}
		//ct := parseTime(resp.Issues[0].Fields.Created)
		//fmt.Printf("ct: %v\n", ct.Format(time.RFC3339))
		//break

		issues := []timedb.IssueFormat1{}
		for _, issue := range resp.Issues {
			issues = append(issues, timedb.IssueFormat1{
				System:      timedb.SystemTypeJira,
				SystemID:    issue.Id,
				Title:       issue.Fields.Summary,
				Type:        parseIssueType(issue.Fields.IssueType.Name),
				StoryPoints: int(issue.Fields.StoryPoints),
				CreateTime:  parseTime(issue.Fields.Created),
			})
		}
		if err = db.InsertIssues1(issues); err != nil {
			return err
		}
		offset += len(issues)
	}
	return nil
}

func (f *Fetcher) Name() string {
	return "JIRA"
}

func (f *Fetcher) FetchRaw(start, end time.Time) ([]byte, error) {
	return f.fetchUrl(f.Config.URL + "/")
}

func (f *Fetcher) fetchUrl(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(f.Config.Username, f.Config.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Error reading JIRA HTTP response body: %v", err)
	}

	return body, nil
}
