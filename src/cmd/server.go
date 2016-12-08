package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"
	"timedb"
)

const listenPort = 3333
const historyDays = 365

const homeRaw = `
<!DOCTYPE html>
<html>
<head>
	<script src='/js/jzed.js'></script>
	<script src='/js/chartist.min.js'></script>
	<link rel='stylesheet' href='/css/chartist.min.css'>
	<link rel='stylesheet' href='/css/main.css'>
</head>
<body>
<select id='select_user'>
	{{range .Users}}
	<option value="{{.UserID}}">{{.Email}}</option>
	{{end}}
</select>

<div class="ct-chart ct-golden-section" style="width:500px; height: 500px;" id="user_chart"></div>

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

type rootData struct {
	Users []user
}

type userReportDataMonth struct {
	Month          string
	BugSeconds     float64
	FeatureSeconds float64
}

func (m *userReportDataMonth) addTicket(ticketType string, duration time.Duration) {
	switch ticketType {
	case timedb.TicketTypeBug:
		m.BugSeconds += duration.Seconds()
	case timedb.TicketTypeFeature:
		m.FeatureSeconds += duration.Seconds()
	}
}

type userReportData struct {
	Months []userReportDataMonth
}

type serverState struct {
	db *timedb.TimeDB
}

var state serverState

func handleRoot(w http.ResponseWriter, r *http.Request) {
	data := &rootData{}
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
	homeTemplate.Execute(w, &data)
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
func handleUserReport(w http.ResponseWriter, r *http.Request) {
	orgDate := time.Now().Add(-historyDays * 24 * time.Hour)
	userid, _ := strconv.ParseInt(r.FormValue("userid"), 10, 64)
	data := &userReportData{}
	data.Months = make([]userReportDataMonth, 0)
	rows, err := state.db.Conn.Query(`SELECT t.start_time, t.end_time, k.ticket_type FROM times AS t INNER JOIN
tickets AS k ON k.ticketid = t.ticketid
WHERE t.userid = $1 AND t.start_time > $2
ORDER BY t.start_time
`, userid, orgDate)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	month := userReportDataMonth{}
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
			month = userReportDataMonth{}
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

func main() {
	state.db = &timedb.TimeDB{}
	if err := state.db.LoadConfig(); err != nil {
		panic(fmt.Sprintf("Unable to load db config: %v", err))
	}
	if err := state.db.Connect(); err != nil {
		panic(fmt.Sprintf("Unable to connect to db: %v", err))
	}

	http.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.Dir("www/js"))))
	http.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("www/css"))))
	http.HandleFunc("/user", handleUserReport)
	http.HandleFunc("/", handleRoot)
	if err := http.ListenAndServe(fmt.Sprintf(":%v", listenPort), nil); err != nil {
		fmt.Printf("Error listening on %v: %v\n", listenPort, err)
	}
}
