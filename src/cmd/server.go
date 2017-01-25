package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"
	"timedb"
)

const listenPort = 3333
const historyDays = 365

const teamAll = "all teams"

type ConfigTeam struct {
	Name         string
	MembersEmail []string
}

type Config struct {
	Teams []ConfigTeam
}

func (c *Config) Load() error {
	filename := "config/server.json"
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("Error loading config file %v: %v", filename, err)
	}
	if err := json.Unmarshal(bytes, c); err != nil {
		return fmt.Errorf("Error decoding config file %v: %v", filename, err)
	}
	return nil
}

const homeRaw = `
<!DOCTYPE html>
<html>
<head>
	<script src='/js/jzed.js'></script>
	<script src='/js/chartist.min.js'></script>
	<link rel='stylesheet' href='/css/chartist.min.css'>
	<link rel='stylesheet' href='/css/main.css'>
	<style>
		{{if not .ShowUsers}}
		.user-select {
			visibility: hidden;
		}
		{{end}}
	</style>
</head>
<body>

<select id='select_user' class='user-select'>
	{{range .Users}}
	<option value="{{.UserID}}">{{.Email}}</option>
	{{end}}
</select>

<select id='select_team'>
	{{range .Teams}}
	<option value="{{.Name}}">{{.HtmlTitle}}</option>
	{{end}}
</select>

<div class="ct-chart ct-golden-section" style="width:600px; height: 500px;" id="monthly_chart"></div>

<div style="background-color: #5d5; width: 10em; height: 1.5em; padding: 3px">Features</div>
<div style="background-color: #d55; width: 10em; height: 1.5em; padding: 3px">Bugs</div>

</body>
<script src='/js/main.js'></script>
</script>
</html>
`

var homeTemplate *template.Template

func init() {
	homeTemplate = template.Must(template.New("home").Parse(homeRaw))
}

type user struct {
	UserID int64
	Email  string
}

type team struct {
	Name              string
	MembersWithNoData []string // These team members have no entries in the database.
}

func (t *team) HtmlTitle() string {
	if len(t.MembersWithNoData) != 0 {
		return t.Name + "(no data for: " + strings.Join(t.MembersWithNoData, ", ") + ")"
	}
	return t.Name
}

type rootData struct {
	Users     []user
	Teams     []team
	ShowUsers bool
}

type reportDataMonth struct {
	Year           int
	Month          string
	BugSeconds     float64
	FeatureSeconds float64
}

func (m *reportDataMonth) addTicket(ticketType string, duration time.Duration) {
	switch ticketType {
	case timedb.TicketTypeBug:
		m.BugSeconds += duration.Seconds()
	case timedb.TicketTypeFeature:
		m.FeatureSeconds += duration.Seconds()
	}
}

type reportData struct {
	Months []reportDataMonth
}

type serverState struct {
	db            *timedb.TimeDB
	config        Config
	userEmailToID map[string]int64 // email addresses are lower case
}

var state serverState

func (s *serverState) buildUserEmailToID() error {
	s.userEmailToID = map[string]int64{}
	rows, err := state.db.Conn.Query("SELECT userid, email FROM users")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		user := user{}
		if err = rows.Scan(&user.UserID, &user.Email); err != nil {
			return err
		}
		s.userEmailToID[strings.ToLower(user.Email)] = user.UserID
	}
	return nil
}

func (s *serverState) usersInTeam(team string) []int64 {
	ids := []int64{}
	for _, t := range s.config.Teams {
		if t.Name == team || team == teamAll {
			for _, email := range t.MembersEmail {
				if id, ok := s.userEmailToID[strings.ToLower(email)]; ok {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	data := &rootData{}
	data.ShowUsers = r.FormValue("foo") == "bar"

	// Add users
	rows, err := state.db.Conn.Query("SELECT userid, email FROM users ORDER BY lower(email)")
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	for rows.Next() {
		user := user{}
		if err = rows.Scan(&user.UserID, &user.Email); err != nil {
			panic(err)
		}
		data.Users = append(data.Users, user)
	}

	// Add teams
	data.Teams = append(data.Teams, team{Name: teamAll})
	for _, t := range state.config.Teams {
		jt := team{
			Name: t.Name,
		}
		for _, email := range t.MembersEmail {
			if _, ok := state.userEmailToID[strings.ToLower(email)]; !ok {
				jt.MembersWithNoData = append(jt.MembersWithNoData, email)
			}
		}
		data.Teams = append(data.Teams, jt)
	}

	homeTemplate.Execute(w, &data)
}

func parseDuration(src []byte) time.Duration {
	parts := strings.Split(string(src), ":")
	h, _ := strconv.ParseUint(parts[0], 10, 64)
	m, _ := strconv.ParseUint(parts[1], 10, 64)
	s, _ := strconv.ParseUint(parts[2], 10, 64)
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

func handleMonthlyReport(w http.ResponseWriter, r *http.Request) {
	orgDate := time.Now().Add(-historyDays * 24 * time.Hour)
	userid, _ := strconv.ParseInt(r.FormValue("userid"), 10, 64)
	teamName := r.FormValue("team")
	data := &reportData{}
	data.Months = []reportDataMonth{}

	users := []int64{}
	if userid != 0 {
		users = append(users, userid)
	} else if teamName != "" {
		users = state.usersInTeam(teamName)
	} else {
		panic("No team or userid specified")
	}

	script := `
SELECT EXTRACT(YEAR FROM t.start_time) AS year, EXTRACT(MONTH FROM t.start_time) AS month, sum(t.end_time - t.start_time) AS sum, k.ticket_type AS ticket_type FROM times AS t INNER JOIN
tickets AS k ON k.ticketid = t.ticketid
WHERE <useridClause> t.start_time > '<orgdate>'
GROUP BY EXTRACT(YEAR FROM t.start_time), EXTRACT(MONTH FROM t.start_time), k.ticket_type
ORDER BY EXTRACT(YEAR FROM t.start_time), EXTRACT(MONTH FROM t.start_time)`

	useridClause := ""
	if len(users) != 0 {
		useridClause = "t.userid IN ("
		for _, uid := range users {
			useridClause += fmt.Sprintf("%v,", uid)
		}
		if len(useridClause) != 0 {
			// chop off trailing comma
			useridClause = useridClause[:len(useridClause)-1]
		}
		useridClause += ") AND "
	}

	script = strings.Replace(script, "<useridClause>", useridClause, -1)
	script = strings.Replace(script, "<orgdate>", orgDate.Format("2006-01-02"), -1)

	rows, err := state.db.Conn.Query(script)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	month := reportDataMonth{}
	for rows.Next() {
		year := 0
		mon := 0
		sum := []byte{}
		ticket_type := ""
		if err := rows.Scan(&year, &mon, &sum, &ticket_type); err != nil {
			panic(err)
		}
		monString := time.Month(mon).String()
		if year != month.Year || monString != month.Month {
			data.Months = append(data.Months, month)
			month.Year = year
			month.Month = monString
		}
		duration := parseDuration(sum)
		if ticket_type == "bug" {
			month.BugSeconds = duration.Seconds()
		} else if ticket_type == "feat" {
			month.FeatureSeconds = duration.Seconds()
		}
	}
	data.Months = append(data.Months, month)

	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

/*
One could use the following SQL to extract a monthly report:

SELECT EXTRACT(MONTH FROM t.start_time), sum(t.end_time - t.start_time), k.ticket_type FROM times AS t INNER JOIN
tickets AS k ON k.ticketid = t.ticketid
WHERE t.userid = 1 AND t.start_time > '2016-01-01'
GROUP BY EXTRACT(MONTH FROM t.start_time), k.ticket_type
ORDER BY EXTRACT(MONTH FROM t.start_time)

However, I'm doing this in Go code to maintain flexibility
*/
// YEAH -- the SQL script is OK.
/*
func handleUserReport(w http.ResponseWriter, r *http.Request) {
	orgDate := time.Now().Add(-historyDays * 24 * time.Hour)
	userid, _ := strconv.ParseInt(r.FormValue("userid"), 10, 64)
	data := &reportData{}
	data.Months = make([]reportDataMonth, 0)
	rows, err := state.db.Conn.Query(`SELECT t.start_time, t.end_time, k.ticket_type FROM times AS t INNER JOIN
tickets AS k ON k.ticketid = t.ticketid
WHERE t.userid = $1 AND t.start_time > $2
ORDER BY t.start_time
`, userid, orgDate)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	month := reportDataMonth{}
	for rows.Next() {
		var start_time, end_time time.Time
		var ticket_type string
		if err := rows.Scan(&start_time, &end_time, &ticket_type); err != nil {
			panic(err)
		}
		if month.Month == "" || start_time.Month().String() != month.Month {
			// new month
			if month.Month != "" {
				data.Months = append(data.Months, month)
			}
			month = reportDataMonth{}
			month.Month = start_time.Month().String()
		}
		month.addTicket(ticket_type, end_time.Sub(start_time))
	}
	if month.Month != "" {
		data.Months = append(data.Months, month)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
*/

func main() {
	state.db = &timedb.TimeDB{}
	if err := state.config.Load(); err != nil {
		panic(fmt.Sprintf("Unable to load server config: %v", err))
	}
	if err := state.db.LoadConfig(); err != nil {
		panic(fmt.Sprintf("Unable to load db config: %v", err))
	}
	if err := state.db.Connect(); err != nil {
		panic(fmt.Sprintf("Unable to connect to db: %v", err))
	}
	if err := state.buildUserEmailToID(); err != nil {
		panic(fmt.Sprintf("Unable to read user list: %v", err))
	}

	http.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.Dir("www/js"))))
	http.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("www/css"))))
	http.HandleFunc("/user", handleMonthlyReport)
	http.HandleFunc("/monthly", handleMonthlyReport)
	http.HandleFunc("/", handleRoot)
	if err := http.ListenAndServe(fmt.Sprintf(":%v", listenPort), nil); err != nil {
		fmt.Printf("Error listening on %v: %v\n", listenPort, err)
	}
}
